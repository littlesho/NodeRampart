// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/replay"
	"github.com/littlesho/NodeRampart/internal/store"
)

func (m *Manager) Action(ctx context.Context, action string, input map[string]string) (string, error) {
	if len(input) > 16 {
		return "", errors.New("too many management arguments")
	}
	for key, value := range input {
		if len(key) > 64 || len(value) > maxManagedJSON || strings.IndexByte(value, 0) >= 0 {
			return "", errors.New("invalid management argument")
		}
	}
	// Removal owns the lock in its already-installed helper, which keeps running
	// after the package manager removes its pathname.
	if action == "uninstall" || action == "purge" {
		confirm := "REMOVE"
		if action == "purge" {
			confirm = "PURGE"
		}
		if input["confirm"] != confirm {
			return "", errors.New("type the displayed confirmation word before removal")
		}
		helper := "/usr/libexec/noderampart/manage-remove"
		if strings.HasPrefix(m.binary, "/usr/local/") {
			helper = "/usr/local/libexec/noderampart/manage-remove"
		}
		if _, err := readFile(helper, 256<<10, false, -1); err != nil {
			return "", errors.New("trusted installed removal helper is unavailable; use the native package manager")
		}
		args := []string{}
		if action == "purge" {
			args = append(args, "--purge")
		}
		result, err := m.command(ctx, helper, args...)
		if err != nil {
			return "", err
		}
		return result + "\nRemoval finished. Exit this menu. / 卸载完成，请退出菜单。", nil
	}
	mutates := map[string]bool{"service_start": true, "service_stop": true, "service_restart": true, "telegram_setup": true,
		"privacy_key_generate": true, "geo_download": true, "geo_refresh": true, "geo_schedule": true,
		"prices_fetch": true, "billing_profile_save": true, "recover_config": true}
	if mutates[action] {
		release, err := m.lock()
		if err != nil {
			return "", err
		}
		defer release()
		if err := ensureDirectory(filepath.Dir(m.ConfigPath), 0o750, m.daemonGID); err != nil {
			return "", err
		}
	}
	switch action {
	case "evidence_export":
		return m.exportEvidence(ctx, input)
	case "doctor":
		return m.command(ctx, m.binary, "doctor", "--config", m.ConfigPath)
	case "service_start", "service_stop", "service_restart":
		return m.serviceAction(ctx, action)
	case "recover_config":
		if input["confirm"] != "RESTORE" {
			return "", errors.New("type RESTORE to recover the previous configuration")
		}
		return m.recoverConfig(ctx)
	case "telegram_setup":
		return m.telegram(ctx, input)
	case "privacy_key_generate":
		return m.generateKey(ctx)
	case "geo_download", "geo_refresh":
		return m.updateGeo(ctx, input, action == "geo_refresh")
	case "geo_schedule":
		if input["enabled"] != "yes" && input["enabled"] != "no" {
			return "", errors.New("select whether daily GeoIP updates are enabled")
		}
		return m.scheduleGeo(ctx, input["enabled"] == "yes")
	case "geo_status":
		return m.geoStatus(ctx)
	case "prices_regions":
		regions, err := m.Assets.Regions(ctx)
		if err != nil {
			return "", err
		}
		return pretty(map[string]any{"aws": regions, "oci": []string{"north-america-europe-uk", "asia-pacific-japan-south-america", "middle-east-africa"}}), nil
	case "prices_fetch":
		return m.fetchPrice(ctx, input)
	case "prices_show":
		return m.showPrices(ctx)
	case "billing_profile_save":
		var p billing.Profile
		decoder := json.NewDecoder(strings.NewReader(input["profile_json"]))
		decoder.DisallowUnknownFields()
		var trailing any
		if len(input["profile_json"]) > 256<<10 || decoder.Decode(&p) != nil || decoder.Decode(&trailing) != io.EOF || p.Validate() != nil {
			return "", errors.New("billing profile fields or tiers are invalid")
		}
		return m.useProfile(ctx, p)
	case "backup_verify":
		value, err := store.VerifyBackup(ctx, input["input"])
		if err != nil {
			return "", errors.New("backup verification failed; select an owned regular backup file")
		}
		return pretty(value), nil
	case "replay_anonymize":
		result, err := replay.Anonymize(ctx, input["input"], input["output"])
		if err != nil {
			return "", err
		}
		return pretty(result), nil
	case "replay_compare":
		a, err := replay.LoadRules(input["baseline"])
		if err != nil {
			return "", errors.New("baseline rules are invalid or unreadable")
		}
		b, err := replay.LoadRules(input["candidate"])
		if err != nil {
			return "", errors.New("candidate rules are invalid or unreadable")
		}
		result, err := replay.Compare(ctx, input["input"], a, b)
		if err != nil {
			return "", err
		}
		return pretty(result), nil
	default:
		return m.query(ctx, action, input)
	}
}

func pageLimit(input map[string]string) (int, error) {
	if input["limit"] == "" {
		return 20, nil
	}
	n, err := strconv.Atoi(input["limit"])
	if err != nil || !api.ValidLimit(n) {
		return 0, errors.New("page size must be 1..100")
	}
	return n, nil
}

func queryTime(input map[string]string, key string, fallback time.Time) (time.Time, error) {
	if input[key] == "" {
		return fallback, nil
	}
	value, err := time.Parse(time.RFC3339Nano, input[key])
	if err != nil {
		return time.Time{}, errors.New("use an RFC3339 timestamp, for example 2026-09-12T00:00:00Z")
	}
	return value.UTC(), nil
}

func (m *Manager) query(ctx context.Context, action string, input map[string]string) (string, error) {
	command := action
	var args any = struct{}{}
	now := time.Now().UTC()
	limit, err := pageLimit(input)
	if err != nil {
		return "", err
	}
	start, err := queryTime(input, "since", now.Add(-7*24*time.Hour))
	if err != nil {
		return "", err
	}
	end, err := queryTime(input, "until", now)
	if err != nil {
		return "", err
	}
	switch action {
	case "status", "doctor", "report_now", "notify_status", "notify_test", "alerts_status":
	case "retention":
		var before int64
		if input["before_id"] != "" {
			before, err = strconv.ParseInt(input["before_id"], 10, 64)
			if err != nil {
				return "", errors.New("retention cursor must be a nonnegative integer")
			}
		}
		query := store.RetentionQuery{Start: start, End: end, Dataset: input["dataset"], Reason: input["reason"], BeforeID: before, Limit: limit}
		if query.Validate() != nil {
			return "", errors.New("invalid retention query period, filters or page size")
		}
		args = query
	case "health":
		if input["since"] == "" {
			start = now.Add(-24 * time.Hour)
		}
		offset := 0
		if input["offset"] != "" {
			offset, err = strconv.Atoi(input["offset"])
			if err != nil || offset < 0 || offset > 20256 {
				return "", errors.New("health offset must be 0..20256; use next_offset from the previous page")
			}
		}
		if input["limit"] == "" {
			limit = 100
		}
		args = api.HealthArgs{Start: start, End: end, Limit: limit, Offset: offset}
	case "notify_list":
		args = api.ListArgs{Before: input["before"], Limit: limit}
	case "report_list":
		args = api.ListArgs{Before: input["before"], Limit: limit}
	case "report_show":
		args = api.DateArgs{Date: input["date"]}
	case "report_backfill":
		args = api.BackfillArgs{From: input["from"], Through: input["through"]}
	case "incident_list":
		before, err := queryTime(input, "before", time.Time{})
		if err != nil {
			return "", err
		}
		args = api.IncidentListArgs{Start: start, End: end, BeforeTime: before, BeforeID: input["before_id"], Limit: limit}
	case "incident_show", "timeline":
		after, err := queryTime(input, "after", time.Time{})
		if err != nil {
			return "", err
		}
		id := input["id"]
		if action == "timeline" {
			command, id = "events_timeline", input["incident"]
		}
		args = api.TimelineArgs{Start: start, End: end, IncidentID: id, AfterTime: after, AfterID: input["after_id"], Limit: limit}
	case "silence_list":
		command = "notify_silence_list"
	case "silence_add":
		if input["until"] == "" {
			return "", errors.New("silences require an explicit expiry")
		}
		command = "notify_silence_add"
		args = api.SilenceArgs{IncidentID: input["incident"], Kind: input["kind"], ExpiresAt: end, Reason: input["reason"]}
	case "silence_remove", "notify_retry", "notify_quarantine":
		if action == "silence_remove" {
			command = "notify_silence_remove"
		}
		args = api.IDArgs{ID: input["id"]}
	case "notify_resume":
		args = api.DestinationArgs{Destination: input["destination"]}
	case "backup_create":
		args = api.BackupArgs{Output: input["output"]}
	default:
		return "", errors.New("unsupported management action")
	}
	if m.Request == nil {
		return "", errors.New("daemon connection is unavailable")
	}
	value, err := m.Request(ctx, command, args)
	if err != nil {
		if action == "status" || action == "doctor" {
			states, localErr := m.serviceStates(ctx)
			if localErr == nil {
				return "Daemon is unavailable; local service state / 守护进程不可用，本地服务状态:\n" + pretty(states), nil
			}
		}
		if len(value) > 0 {
			return string(value), err // Preserve bounded backfill progress.
		}
		return "", err
	}
	if action == "report_show" || action == "report_now" {
		return formatReport(value, action == "report_show")
	}
	if action == "status" || action == "doctor" {
		return formatStatus(value), nil
	}
	var output bytes.Buffer
	if json.Indent(&output, value, "", "  ") != nil {
		return "", errors.New("daemon returned an invalid result")
	}
	return output.String(), nil
}

func formatStatus(data []byte) string {
	var value struct {
		Version         struct{ Version, Commit string }
		Uptime          string
		TelegramEnabled bool `json:"telegram_enabled"`
		GeoEnabled      bool `json:"geo_enabled"`
		Batches         uint64
		Components      []struct{ Name, State string }
		Storage         struct {
			Healthy  bool
			Failures uint64
		}
	}
	if json.Unmarshal(data, &value) != nil {
		return "Status unavailable"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "NodeRampart %s\nUptime / 运行时间: %s\nSensor batches / 采集批次: %d\nTelegram: %t    GeoIP: %t\nStorage healthy / 存储健康: %t (failures / 失败次数: %d)\n\n", value.Version.Version, value.Uptime, value.Batches, value.TelegramEnabled, value.GeoEnabled, value.Storage.Healthy, value.Storage.Failures)
	for _, component := range value.Components {
		fmt.Fprintf(&out, "%-24s %s\n", component.Name, component.State)
	}
	var detail bytes.Buffer
	_ = json.Indent(&detail, data, "", "  ")
	out.WriteString("\nDetailed evidence / 详细信息:\n" + detail.String())
	return out.String()
}

var telegramToken = regexp.MustCompile(`^[0-9]{6,16}:[A-Za-z0-9_-]{30,128}$`)

func (m *Manager) telegram(ctx context.Context, input map[string]string) (string, error) {
	snapshot, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	if input["enabled"] != "yes" && input["enabled"] != "no" {
		return "", errors.New("choose whether Telegram is enabled")
	}
	snapshot.Config.Notifications.Telegram.Enabled = input["enabled"] == "yes"
	if input["chat_id"] != "" {
		snapshot.Config.Notifications.Telegram.ChatID = input["chat_id"]
	}
	created := ""
	if input["token"] != "" {
		if !telegramToken.MatchString(input["token"]) {
			return "", errors.New("Telegram token format is invalid; obtain it from BotFather")
		}
		created, err = m.newSecret("telegram", []byte(input["token"]+"\n"))
		if err != nil {
			return "", err
		}
		snapshot.Config.Notifications.Telegram.TokenFile = created
	}
	result, err := m.saveLocked(ctx, snapshot)
	if err != nil && created != "" {
		m.removeUnreferencedSecret(created)
	}
	if err == nil && created != "" {
		if cleanupErr := m.cleanManagedFiles(filepath.Dir(created), "telegram-", ".secret", created, snapshot.Config.Privacy.HashKeyFile); cleanupErr != nil {
			return result, errors.New("Telegram configured, but old managed token cleanup failed")
		}
	}
	return result, err
}

func (m *Manager) newSecret(kind string, data []byte) (string, error) {
	dir := filepath.Join(filepath.Dir(m.ConfigPath), "secrets")
	if err := ensureDirectory(dir, 0o750, m.daemonGID); err != nil {
		return "", err
	}
	current, err := m.Load(context.Background())
	if err != nil {
		return "", err
	}
	if err := m.cleanManagedFiles(dir, kind+"-", ".secret", current.Config.Notifications.Telegram.TokenFile, current.Config.Privacy.HashKeyFile); err != nil {
		return "", err
	}
	path := filepath.Join(dir, kind+"-"+rand.Text()+".secret")
	if err := writeFile(path, bytes.NewReader(data), 4096, 0o600, m.daemonUID, m.daemonGID, false); err != nil {
		return "", err
	}
	return path, nil
}

func (m *Manager) removeUnreferencedSecret(path string) {
	current, err := m.Load(context.Background())
	if err == nil && current.Config.Notifications.Telegram.TokenFile != path && current.Config.Privacy.HashKeyFile != path {
		_ = removeFile(path)
	}
}

func (m *Manager) generateKey(ctx context.Context) (string, error) {
	snapshot, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	if snapshot.Config.Privacy.HashKeyFile != "" {
		return "", errors.New("a hash key is already configured; generation will not replace it or change historical identity")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", errors.New("privacy key generation failed")
	}
	path, err := m.newSecret("privacy", []byte(hex.EncodeToString(key)+"\n"))
	if err != nil {
		return "", err
	}
	snapshot.Config.Privacy.HashKeyFile = path
	result, err := m.saveLocked(ctx, snapshot)
	if err != nil {
		m.removeUnreferencedSecret(path)
	}
	return result, err
}

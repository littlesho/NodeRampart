// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"golang.org/x/sys/unix"
)

func officialActionChannel(action string) (string, string) {
	for _, suffix := range []string{"_setup", "_credentials", "_subscription"} {
		if strings.HasSuffix(action, suffix) {
			channel := strings.TrimSuffix(action, suffix)
			if config.IsOfficialChannel(channel) {
				return channel, strings.TrimPrefix(suffix, "_")
			}
		}
	}
	return "", ""
}

func (m *Manager) readOfficialCredential(channel, path string) (config.OfficialCredential, error) {
	var c config.OfficialCredential
	invalid := errors.New("official credentials are missing, unsafe or invalid / 官方渠道凭据缺失、不安全或无效")
	base := filepath.Dir(m.ConfigPath)
	if filepath.Dir(path) != base && filepath.Dir(path) != filepath.Join(base, "secrets") {
		return c, invalid
	}
	f, before, err := openManagedCredentialFile(path, config.MaxOfficialCredentialBytes, m.daemonUID)
	if err != nil {
		return c, invalid
	}
	defer f.Close()
	mode := before.Mode & 0o7777
	if (before.Uid != uint32(m.daemonUID) && before.Uid != 0) || (mode != 0o600 && !(mode == 0o640 && before.Uid == 0 && before.Gid == uint32(m.daemonGID))) || m.daemonReadable(path, false) != nil {
		return c, invalid
	}
	data, err := io.ReadAll(io.LimitReader(f, config.MaxOfficialCredentialBytes+1))
	var after unix.Stat_t
	if err != nil || len(data) > config.MaxOfficialCredentialBytes || unix.Fstat(int(f.Fd()), &after) != nil || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim || before.Mode != after.Mode || before.Uid != after.Uid || before.Gid != after.Gid || before.Nlink != after.Nlink || before.Dev != after.Dev || before.Ino != after.Ino {
		return c, invalid
	}
	return config.DecodeOfficialCredential(channel, data)
}

func officialBool(input map[string]string, key string, previous bool) (bool, error) {
	v, ok := input[key]
	if !ok {
		return previous, nil
	}
	if v != "yes" && v != "no" {
		return false, errors.New("choose yes or no / 请选择是或否")
	}
	return v == "yes", nil
}

func officialLimit(input map[string]string, key string, previous int) (int, error) {
	v, ok := input[key]
	if !ok {
		return previous, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || len(v) > 4 || n < 0 {
		return 0, errors.New("notification limit must be a bounded nonnegative integer; zero forbids sending / 通知限额须为有界非负整数；零禁止发送")
	}
	return n, nil
}

func (m *Manager) official(ctx context.Context, channel, operation string, input map[string]string) (string, error) {
	snapshot, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	c := snapshot.Config.Notifications.OfficialChannels()[channel]
	created, clearCredential := "", false
	allowed := map[string]bool{}
	switch operation {
	case "setup":
		for _, key := range []string{"enabled", "language", "events_enabled", "daily_enabled", "min_severity", "daily_message_limit", "daily_segment_limit", "max_segments"} {
			allowed[key] = true
		}
		for key, destination := range map[string]*bool{"enabled": &c.Enabled, "events_enabled": &c.EventsEnabled, "daily_enabled": &c.DailyEnabled} {
			value, e := officialBool(input, key, *destination)
			if e != nil {
				return "", e
			}
			*destination = value
		}
		if v, ok := input["language"]; ok {
			c.Language = v
		}
		if v, ok := input["min_severity"]; ok {
			c.MinSeverity = v
		}
		for key, destination := range map[string]*int{"daily_message_limit": &c.DailyMessageLimit, "daily_segment_limit": &c.DailySegmentLimit, "max_segments": &c.MaxSegments} {
			value, e := officialLimit(input, key, *destination)
			if e != nil {
				return "", e
			}
			*destination = value
		}
	case "credentials":
		allowed["credential_action"], allowed["credential_json"], allowed["clear_fields"] = true, true, true
		for _, field := range config.OfficialCredentialFieldNames(channel) {
			allowed[field] = true
		}
		mode := input["credential_action"]
		if mode == "" {
			mode = "keep"
		}
		if mode != "keep" && mode != "replace" && mode != "clear" {
			return "", errors.New("choose keep, replace or clear before entering hidden credentials / 填写隐藏凭据前须选择保留、替换或清空")
		}
		for _, field := range append(config.OfficialCredentialFieldNames(channel), "credential_json", "clear_fields") {
			if mode != "replace" && input[field] != "" {
				return "", errors.New("select replace before editing or clearing credential fields / 修改或清空凭据字段前请选择替换")
			}
		}
		if mode == "clear" {
			if c.Enabled {
				return "", errors.New("disable this channel before clearing credentials / 清空凭据前请停用本渠道")
			}
			c.CredentialFile = ""
			clearCredential = true
		}
		if mode == "replace" {
			credential, e := m.updatedOfficialCredential(channel, c.CredentialFile, input)
			if e != nil {
				return "", e
			}
			if c.Enabled {
				if e := config.ValidateOfficialCredentialPolicy(channel, c, credential); e != nil {
					return "", e
				}
			}
			data, e := json.Marshal(credential)
			if e != nil {
				return "", errors.New("official credentials could not be encoded / 官方渠道凭据无法编码")
			}
			created, e = m.newSecret(channel, append(data, '\n'))
			if e != nil {
				return "", e
			}
			c.CredentialFile = created
		}
	case "subscription":
		for _, key := range []string{"consent_action", "purpose", "notification_types", "evidence_ref", "cost_confirmed", "platform_recovery_confirmed"} {
			allowed[key] = true
		}
		mode := input["consent_action"]
		if mode == "" {
			mode = "keep"
		}
		if mode != "keep" && mode != "record" && mode != "revoke" {
			return "", errors.New("choose keep, record new consent or revoke / 请选择保留、记录新同意或撤销")
		}
		if mode != "record" && (input["purpose"] != "" || input["notification_types"] != "" || input["evidence_ref"] != "" || input["cost_confirmed"] != "" && input["cost_confirmed"] != "no" || input["platform_recovery_confirmed"] != "" && input["platform_recovery_confirmed"] != "no") {
			return "", errors.New("new subscription details require an explicit new consent action / 新订阅资料须明确选择记录新同意")
		}
		if mode == "revoke" {
			c.Subscription.Revoked = true
		}
		if mode == "record" {
			cost, e := officialBool(input, "cost_confirmed", false)
			if e != nil {
				return "", e
			}
			recovery, e := officialBool(input, "platform_recovery_confirmed", false)
			if e != nil {
				return "", e
			}
			if recovery && channel != "twilio_sms" {
				return "", errors.New("official opt-out recovery confirmation applies only to Twilio SMS / 官方退订恢复确认仅适用于 Twilio SMS")
			}
			if recovery && input["evidence_ref"] == c.Subscription.EvidenceRef {
				return "", errors.New("opt-out recovery needs a new explicit consent evidence reference / 退订恢复须提供新的明确同意依据编号")
			}
			if config.IsPaidOfficialChannel(channel) && !cost {
				return "", errors.New("paid subscription requires explicit cost confirmation / 付费订阅须明确确认费用")
			}
			var basis [16]byte
			if _, e := rand.Read(basis[:]); e != nil {
				return "", errors.New("subscription identity could not be generated / 无法生成订阅身份")
			}
			c.Subscription = config.OfficialSubscription{ConfirmedAt: time.Now().UTC().Format(time.RFC3339Nano), Purpose: input["purpose"], NotificationTypes: strings.Split(input["notification_types"], ","), EvidenceRef: input["evidence_ref"], BasisID: hex.EncodeToString(basis[:]), CostConfirmed: cost, PlatformRecoveryConfirmed: recovery}
		}
	default:
		return "", errors.New("unsupported official notification operation / 不支持的官方通知操作")
	}
	for key := range input {
		if !allowed[key] {
			if created != "" {
				m.removeUnreferencedSecret(created)
			}
			return "", errors.New("unsupported official notification argument / 不支持的官方通知参数")
		}
	}
	if err := config.ValidateOfficialChannel(channel, c); err != nil {
		if created != "" {
			m.removeUnreferencedSecret(created)
		}
		return "", err
	}
	if err := snapshot.Config.Notifications.SetOfficialChannel(channel, c); err != nil {
		if created != "" {
			m.removeUnreferencedSecret(created)
		}
		return "", err
	}
	result, err := m.saveLocked(ctx, snapshot)
	if err != nil && created != "" {
		m.removeUnreferencedSecret(created)
	}
	if err == nil && (created != "" || clearCredential) {
		dir := filepath.Join(filepath.Dir(m.ConfigPath), "secrets")
		if _, statErr := os.Lstat(dir); !errors.Is(statErr, os.ErrNotExist) {
			if m.cleanManagedFiles(dir, channel+"-", ".secret", secretReferences(snapshot.Config)...) != nil {
				return result, errors.New("settings saved; old managed credential cleanup needs inspection / 设置已保存；旧托管凭据清理需检查")
			}
		}
	}
	if err == nil {
		result += "\nOfficial channel settings saved locally; no message was sent. Changed credentials or policy isolate old pending messages. Revocation suppresses pending work. Paid tests require their own preview and cost confirmation. In-flight requests can finish at the original target. A recorded subscription does not replace platform approval or recipient consent.\n官方渠道设置已在本地保存，未发送消息。修改凭据或策略会隔离旧积压；撤销订阅会抑制未发送任务。付费测试仍须独立预览并确认费用。发送中的请求可能在原目标完成。记录订阅不能替代平台审批或接收者同意。"
	}
	return result, err
}

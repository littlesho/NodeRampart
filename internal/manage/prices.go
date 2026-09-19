// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/report"
	"github.com/littlesho/NodeRampart/internal/store"
)

func (m *Manager) fetchPrice(ctx context.Context, input map[string]string) (string, error) {
	free := 0.0
	var err error
	if input["free_gb"] != "" {
		free, err = strconv.ParseFloat(input["free_gb"], 64)
	}
	if err != nil || free < 0 || math.IsNaN(free) || math.IsInf(free, 0) {
		return "", errors.New("assigned monthly free allowance must be a finite nonnegative number")
	}
	unit := uint64(1 << 30)
	if input["unit_bytes"] != "" {
		unit, err = strconv.ParseUint(input["unit_bytes"], 10, 64)
	}
	if err != nil || unit != 1<<30 && unit != 1_000_000_000 {
		return "", errors.New("calculation unit must be 1073741824 or 1000000000 bytes")
	}
	value, err := m.Assets.FetchPrice(ctx, input["provider"], input["region"])
	if err != nil {
		return "", err
	}
	if free > value.PublishedFreeGB {
		return "", fmt.Errorf("assigned allowance exceeds the catalog's published shared allowance %.3f GB", value.PublishedFreeGB)
	}
	value.Profile.FreeGB, value.Profile.UnitBytes = free, unit
	result, err := m.useProfile(ctx, value.Profile)
	if err != nil {
		return "", err
	}
	if err := m.writeJSON(m.localPath("prices-"+value.Provider+".json"), value, true); err != nil {
		return result, errors.New("pricing profile activated, but catalog cache could not be updated; the old cache may remain")
	}
	return fmt.Sprintf("Official catalog fetched and applied to future reports / 已获取官方价格并应用于后续报告\n%s\nAssigned monthly free allowance / 分配给本机本月的免费额度: %.3f GB\nCalculation assumption / 计算假设: %d bytes per GB\n%s", pretty(value), free, unit, result), nil
}

func (m *Manager) useProfile(ctx context.Context, profile billing.Profile) (string, error) {
	if err := profile.Validate(); err != nil {
		return "", errors.New("billing profile is invalid")
	}
	snapshot, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	dir := m.localPath("prices")
	if err := ensureDirectory(dir, 0o750, m.daemonGID); err != nil {
		return "", err
	}
	if err := m.cleanManagedFiles(dir, "profile-", ".json", snapshot.Config.Billing.ProfilePath); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "profile-"+rand.Text()+".json")
	data, _ := json.MarshalIndent(profile, "", "  ")
	if err := writeFile(path, strings.NewReader(string(data)+"\n"), 256<<10, 0o640, os.Geteuid(), m.daemonGID, false); err != nil {
		return "", err
	}
	snapshot.Config.Billing.Enabled, snapshot.Config.Billing.ProfilePath = true, path
	result, err := m.saveLocked(ctx, snapshot)
	if current, loadErr := m.Load(context.Background()); loadErr == nil {
		if cleanupErr := m.cleanManagedFiles(dir, "profile-", ".json", current.Config.Billing.ProfilePath); err == nil && cleanupErr != nil {
			return result, errors.New("profile activated, but old managed profile cleanup failed")
		}
	}
	return result, err
}

func (m *Manager) cleanManagedFiles(dir, prefix, suffix string, keep ...string) error {
	if _, err := readFile(m.journalPath(), 2*maxManagedJSON, true, -1); !errors.Is(err, os.ErrNotExist) {
		return errors.New("pending configuration recovery pins previous managed files")
	}
	directory, err := openDirectory(dir, true)
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(129)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > 128 {
		return errors.New("managed directory needs manual inspection")
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		path, retained := filepath.Join(dir, entry.Name()), false
		for _, active := range keep {
			if active == path {
				retained = true
			}
		}
		if !retained {
			if _, err := readFile(path, maxManagedJSON, false, m.daemonUID); err != nil {
				return err
			}
			if err := removeFile(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) showPrices(ctx context.Context) (string, error) {
	snapshot, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	location, err := config.ReportLocation(snapshot.Config.Reports)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	start, ok := report.MonthStart(now, location)
	if !ok || !start.Before(now) {
		return "", errors.New("current report month has no measurable period yet")
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Observed month-to-date egress estimate / 本月已观测出站费用估算\n%s — %s (%s)\n", start.Format(time.RFC3339), now.Format(time.RFC3339), location)
	var evidence struct {
		History store.IntegrityView      `json:"history"`
		Traffic store.InterfaceBreakdown `json:"interface_traffic"`
	}
	available := false
	if m.Request != nil {
		data, err := m.Request(ctx, "health", api.HealthArgs{Start: start, End: now, Limit: 1})
		available = err == nil && json.Unmarshal(data, &evidence) == nil && evidence.Traffic.Start.Equal(start) && evidence.Traffic.End.Equal(now)
	}
	if available {
		fmt.Fprintf(&out, "Guest interface TX / 主机网卡出站: %d bytes\n", evidence.Traffic.Total.TXBytes)
	} else {
		out.WriteString("Usage unavailable; tariff information only. / 用量不可用，仅显示价格。\n")
	}
	display := func(label string, profile *billing.Profile) {
		unit := profile.UnitBytes
		if unit == 0 {
			unit = 1_000_000_000
		}
		fmt.Fprintf(&out, "\n%s\n%s / %s / %s\nSource / 来源: %s\nEffective date / 生效日期: %s\nAssigned monthly free allowance / 本机本月免费额度: %.3f GB\nCalculation assumption / 计算假设: %d bytes per GB\n", label, profile.Name, profile.Provider, profile.SourceRegion, profile.SourceURL, profile.EffectiveDate, profile.FreeGB, unit)
		if available {
			out.WriteString(profile.Estimate(evidence.Traffic.Total.TXBytes).String() + "\n")
		}
		out.WriteString("Tiers / 阶梯: " + pretty(profile.InternetEgress) + "\n")
	}
	if snapshot.Config.Billing.Enabled {
		if profile, err := billing.Load(snapshot.Config.Billing.ProfilePath); err == nil {
			display("Active daily-report profile / 当前日报配置", profile)
		} else {
			out.WriteString("Active billing profile unavailable. / 当前费用配置不可用。\n")
		}
	}
	cacheFound := false
	for _, provider := range []string{"aws", "oci"} {
		data, err := readFile(m.localPath("prices-"+provider+".json"), maxManagedJSON, true, -1)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		var cached assets.PriceSnapshot
		if err != nil || json.Unmarshal(data, &cached) != nil || cached.SchemaVersion != 1 || cached.Provider != provider || cached.Profile.Validate() != nil || cached.FetchedAt.IsZero() {
			fmt.Fprintf(&out, "\n%s cache is invalid or unreadable. / 缓存无效或不可读。\n", provider)
			continue
		}
		display("Cached catalog scenario / 缓存价格试算", &cached.Profile)
		cacheFound = true
		fmt.Fprintf(&out, "Fetched / 获取时间: %s (age / 距今: %s)\nPublished / 发布时间: %s\nEffective / 生效时间: %s\nSource SHA256: %s\nPublished SHARED allowance / 官方共享额度: %.3f GB\n", cached.FetchedAt.Format(time.RFC3339), now.Sub(cached.FetchedAt).Round(time.Hour), cached.PublishedAt.Format(time.RFC3339), cached.EffectiveAt.Format(time.RFC3339), cached.Digest, cached.PublishedFreeGB)
		if now.Sub(cached.FetchedAt) > 7*24*time.Hour {
			out.WriteString("STALE: cache exceeds 7 days; refresh explicitly. / 缓存超过 7 天，请手动刷新。\n")
		}
		for _, note := range cached.Notes {
			out.WriteString(note + "\n")
		}
	}
	if !cacheFound {
		out.WriteString("\nNo valid catalog cache yet; choose Fetch and apply official pricing to select a provider and region. / 暂无有效价格缓存，请选择获取并应用官方价格，设置提供商和区域。\n")
	}
	out.WriteString("\nAssign this host a monthly allowance after other services/hosts, without subtracting this host’s observed usage again.\n本机额度可预扣其他主机或服务占用，请勿再扣本机已观测用量。\nEstimate only: guest TX includes internal traffic and may count tunnels/bridges more than once. Cloud meters, shared account tiers/allowances and overlapping UTC-hour counters differ; missing data is not zero usage. No instance/storage/NAT/tax costs or full-month projection.\n仅为估算：网卡出站可能包括内网流量和重复路径；云端计量、账号共享阶梯及额度可能不同，数据缺口不等于零用量。不含实例、存储、NAT、税费，也不是整月预测。\n")
	if available {
		fmt.Fprintf(&out, "\nCoverage / 覆盖情况:\n%s\nHistory truncated: %t; gaps truncated: %t; loss hours truncated: %t; retained gaps: %d\n", pretty(evidence.History.Components), evidence.History.HistoryTruncated, evidence.History.GapsTruncated, evidence.History.LossHoursTruncated, len(evidence.History.Gaps))
		for _, note := range evidence.Traffic.Notes {
			out.WriteString(note + "\n")
		}
	}
	return out.String(), nil
}

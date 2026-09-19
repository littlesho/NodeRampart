// SPDX-License-Identifier: MIT

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/protocol"
)

const maxConfigSize = 1 << 20

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("duration must be a string such as \"10s\"")
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

type Config struct {
	SchemaVersion int                 `json:"schema_version"`
	Hostname      string              `json:"hostname"`
	Paths         PathsConfig         `json:"paths"`
	Storage       StorageConfig       `json:"storage"`
	Sensor        SensorConfig        `json:"sensor"`
	Auth          AuthConfig          `json:"auth"`
	Detection     DetectionConfig     `json:"detection"`
	Geo           GeoConfig           `json:"geo"`
	Notifications NotificationsConfig `json:"notifications"`
	Reports       ReportsConfig       `json:"reports"`
	Privacy       PrivacyConfig       `json:"privacy"`
	Billing       BillingConfig       `json:"billing"`
	Alerts        AlertsConfig        `json:"alerts"`
}

type PathsConfig struct {
	Database      string `json:"database"`
	SensorSocket  string `json:"sensor_socket"`
	ControlSocket string `json:"control_socket"`
}

type StorageConfig struct {
	MaxBytes     int64 `json:"max_bytes"`
	MinFreeBytes int64 `json:"min_free_bytes"`
}

type SensorConfig struct {
	Enabled            bool     `json:"enabled"`
	Required           bool     `json:"required"`
	Interface          string   `json:"interface"`
	Interfaces         []string `json:"interfaces,omitempty"`
	BatchInterval      Duration `json:"batch_interval"`
	MaxTrackedFlows    int      `json:"max_tracked_flows"`
	ReceiveBufferBytes int      `json:"receive_buffer_bytes"`
}

type AuthConfig struct {
	Enabled    bool     `json:"enabled"`
	Journalctl string   `json:"journalctl"`
	Threshold  int      `json:"threshold"`
	Window     Duration `json:"window"`
	Cooldown   Duration `json:"cooldown"`
}

func (c SensorConfig) InterfaceNames() []string {
	if c.Interface != "" {
		return []string{c.Interface}
	}
	return append([]string(nil), c.Interfaces...)
}

func (c SensorConfig) InterfaceLimit() int {
	if c.Interface != "" {
		return 1
	}
	if len(c.Interfaces) > 0 {
		return len(c.Interfaces)
	}
	return 2 // One selected IPv4 and one selected IPv6 main-table route.
}

type DetectionConfig struct {
	SYNPacketsPerSecond  uint64   `json:"syn_packets_per_second"`
	UDPPacketsPerSecond  uint64   `json:"udp_packets_per_second"`
	ICMPPacketsPerSecond uint64   `json:"icmp_packets_per_second"`
	BytesPerSecond       uint64   `json:"bytes_per_second"`
	RecoveryRatio        float64  `json:"recovery_ratio"`
	RecoveryWindows      int      `json:"recovery_windows"`
	UpdateInterval       Duration `json:"update_interval"`
	ScanUniquePorts      int      `json:"scan_unique_ports"`
	ScanWindow           Duration `json:"scan_window"`
}

type GeoConfig struct {
	CityMMDB string `json:"city_mmdb"`
	ASNMMDB  string `json:"asn_mmdb"`
}

type NotificationsConfig struct {
	Telegram    TelegramConfig `json:"telegram"`
	MergeWindow Duration       `json:"merge_window"`
}

type TelegramConfig struct {
	Enabled   bool     `json:"enabled"`
	TokenFile string   `json:"token_file"`
	ChatID    string   `json:"chat_id"`
	Timeout   Duration `json:"timeout"`
}

type ReportsConfig struct {
	Enabled      bool   `json:"enabled"`
	DailyAt      string `json:"daily_at"`
	Timezone     string `json:"timezone"`
	TopN         int    `json:"top_n"`
	BackfillDays int    `json:"backfill_days"`
}

type PrivacyConfig struct {
	NotificationIP string `json:"notification_ip"`
	StoreIP        string `json:"store_ip"`
	HashKeyFile    string `json:"hash_key_file"`
}

type BillingConfig struct {
	Enabled     bool   `json:"enabled"`
	ProfilePath string `json:"profile_path"`
}

func Defaults() Config {
	hostname, _ := os.Hostname()
	return Config{
		SchemaVersion: 1,
		Alerts:        defaultAlerts(),
		Hostname:      hostname,
		Paths: PathsConfig{
			Database:      "/var/lib/noderampart/noderampart.db",
			SensorSocket:  "/run/noderampart/sensor.sock",
			ControlSocket: "/run/noderampart/control.sock",
		},
		Storage: StorageConfig{MaxBytes: 1 << 30, MinFreeBytes: 128 << 20},
		Sensor:  SensorConfig{Enabled: true, BatchInterval: Duration{time.Second}, MaxTrackedFlows: protocol.MaxFlowsPerBatch, ReceiveBufferBytes: 4 << 20},
		Auth:    AuthConfig{Enabled: true, Journalctl: "/usr/bin/journalctl", Threshold: 8, Window: Duration{5 * time.Minute}, Cooldown: Duration{15 * time.Minute}},
		Detection: DetectionConfig{
			SYNPacketsPerSecond: 5000, UDPPacketsPerSecond: 10000, ICMPPacketsPerSecond: 2000,
			BytesPerSecond: 100 * 1024 * 1024, RecoveryRatio: 0.5, RecoveryWindows: 3,
			UpdateInterval: Duration{5 * time.Minute}, ScanUniquePorts: 20, ScanWindow: Duration{60 * time.Second},
		},
		Notifications: NotificationsConfig{Telegram: TelegramConfig{TokenFile: "/etc/noderampart/telegram.token", Timeout: Duration{10 * time.Second}}, MergeWindow: Duration{10 * time.Minute}},
		Reports:       ReportsConfig{Enabled: true, DailyAt: "09:00", Timezone: "Local", TopN: 10, BackfillDays: 7},
		Privacy:       PrivacyConfig{NotificationIP: "prefix", StoreIP: "prefix"},
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	info, err := os.Lstat(path)
	if err != nil {
		return cfg, fmt.Errorf("stat config: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return cfg, errors.New("config must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return cfg, errors.New("config must not be writable by group or others")
	}
	if info.Size() > maxConfigSize {
		return cfg, errors.New("config exceeds 1 MiB limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, maxConfigSize+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return cfg, errors.New("config contains trailing JSON data")
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if err := c.validateAlerts(); err != nil {
		return err
	}
	if c.Notifications.MergeWindow.Duration < 0 || c.Notifications.MergeWindow.Duration > 24*time.Hour {
		return errors.New("notifications.merge_window must be 0..24h")
	}
	var problems []string
	if c.SchemaVersion != 1 {
		problems = append(problems, "schema_version must be 1")
	}
	if c.Storage.MaxBytes < 64<<20 || c.Storage.MaxBytes > 1<<40 {
		problems = append(problems, "storage.max_bytes must be 67108864..1099511627776")
	}
	if c.Storage.MinFreeBytes < 0 || c.Storage.MinFreeBytes > 1<<40 {
		problems = append(problems, "storage.min_free_bytes must be 0..1099511627776")
	}
	if strings.TrimSpace(c.Hostname) == "" || len(c.Hostname) > 255 {
		problems = append(problems, "hostname must be 1..255 characters")
	} else if strings.IndexFunc(c.Hostname, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		problems = append(problems, "hostname must not contain control characters")
	}
	for name, path := range map[string]string{"database": c.Paths.Database, "sensor_socket": c.Paths.SensorSocket, "control_socket": c.Paths.ControlSocket} {
		if !cleanAbsolute(path) {
			problems = append(problems, name+" must be a clean absolute path")
		}
	}
	if len(c.Paths.SensorSocket) > 100 || len(c.Paths.ControlSocket) > 100 {
		problems = append(problems, "Unix socket paths must be at most 100 bytes")
	}
	if strings.Contains(c.Sensor.Interface, "/") || len(c.Sensor.Interface) > 15 || strings.IndexFunc(c.Sensor.Interface, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
		problems = append(problems, "sensor.interface is invalid")
	}
	if len(c.Sensor.Interfaces) > protocol.MaxInterfaces || c.Sensor.Interface != "" && len(c.Sensor.Interfaces) > 0 {
		problems = append(problems, "sensor.interfaces supports up to eight names and is mutually exclusive with sensor.interface")
	}
	seenInterfaces := map[string]bool{}
	for _, name := range c.Sensor.Interfaces {
		if !protocol.ValidInterfaceName(name) || seenInterfaces[name] {
			problems = append(problems, "sensor.interfaces has an invalid or duplicate name")
			break
		}
		seenInterfaces[name] = true
	}
	if c.Sensor.BatchInterval.Duration < 100*time.Millisecond || c.Sensor.BatchInterval.Duration > time.Minute {
		problems = append(problems, "sensor.batch_interval must be between 100ms and 1m")
	}
	if c.Sensor.MaxTrackedFlows < 64 || c.Sensor.MaxTrackedFlows > protocol.MaxFlowsPerBatch {
		problems = append(problems, fmt.Sprintf("sensor.max_tracked_flows must be 64..%d", protocol.MaxFlowsPerBatch))
	}
	if c.Sensor.ReceiveBufferBytes < 64<<10 || c.Sensor.ReceiveBufferBytes > 256<<20 {
		problems = append(problems, "sensor.receive_buffer_bytes must be 65536..268435456")
	}
	if c.Auth.Threshold < 2 || c.Auth.Threshold > 4096 {
		problems = append(problems, "auth.threshold must be 2..4096")
	}
	if c.Auth.Window.Duration < time.Second || c.Auth.Window.Duration > 24*time.Hour {
		problems = append(problems, "auth.window must be 1s..24h")
	}
	if c.Auth.Cooldown.Duration < 0 || c.Auth.Cooldown.Duration > 7*24*time.Hour {
		problems = append(problems, "auth.cooldown must be 0..168h")
	}
	if c.Detection.RecoveryRatio <= 0 || c.Detection.RecoveryRatio >= 1 {
		problems = append(problems, "detection.recovery_ratio must be between 0 and 1")
	}
	if c.Detection.RecoveryWindows < 1 || c.Detection.RecoveryWindows > 60 {
		problems = append(problems, "detection.recovery_windows must be 1..60")
	}
	if c.Detection.UpdateInterval.Duration < time.Second || c.Detection.UpdateInterval.Duration > 24*time.Hour {
		problems = append(problems, "detection.update_interval must be 1s..24h")
	}
	if c.Detection.ScanUniquePorts < 2 || c.Detection.ScanUniquePorts > 65535 {
		problems = append(problems, "detection.scan_unique_ports must be 2..65535")
	}
	if c.Detection.ScanWindow.Duration < time.Second || c.Detection.ScanWindow.Duration > time.Hour {
		problems = append(problems, "detection.scan_window must be 1s..1h")
	}
	for _, value := range []string{c.Privacy.NotificationIP, c.Privacy.StoreIP} {
		if value != "prefix" && value != "full" && value != "hash" {
			problems = append(problems, "privacy IP modes must be prefix, full, or hash")
		}
	}
	if (c.Privacy.NotificationIP == "hash" || c.Privacy.StoreIP == "hash") && !cleanAbsolute(c.Privacy.HashKeyFile) {
		problems = append(problems, "privacy.hash_key_file must be absolute when hash mode is used")
	}
	for name, path := range map[string]string{"privacy.hash_key_file": c.Privacy.HashKeyFile, "geo.city_mmdb": c.Geo.CityMMDB, "geo.asn_mmdb": c.Geo.ASNMMDB} {
		if path != "" && !cleanAbsolute(path) {
			problems = append(problems, name+" must be a clean absolute path")
		}
	}
	if c.Auth.Enabled && c.Auth.Journalctl != "/usr/bin/journalctl" && c.Auth.Journalctl != "/bin/journalctl" {
		problems = append(problems, "auth.journalctl must be /usr/bin/journalctl or /bin/journalctl")
	}
	if c.Notifications.Telegram.Enabled {
		if !cleanAbsolute(c.Notifications.Telegram.TokenFile) {
			problems = append(problems, "telegram.token_file must be a clean absolute path")
		}
		if strings.TrimSpace(c.Notifications.Telegram.ChatID) == "" || len(c.Notifications.Telegram.ChatID) > 128 {
			problems = append(problems, "telegram.chat_id is required and limited to 128 characters")
		} else if strings.IndexFunc(c.Notifications.Telegram.ChatID, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			problems = append(problems, "telegram.chat_id must not contain control characters")
		}
		if c.Notifications.Telegram.Timeout.Duration < time.Second || c.Notifications.Telegram.Timeout.Duration > time.Minute {
			problems = append(problems, "telegram.timeout must be 1s..1m")
		}
	}
	if c.Reports.TopN < 1 || c.Reports.TopN > 50 {
		problems = append(problems, "reports.top_n must be 1..50")
	}
	if c.Reports.BackfillDays < 0 || c.Reports.BackfillDays > 31 {
		return errors.New("reports.backfill_days must be 0..31")
	}
	if _, _, err := parseClock(c.Reports.DailyAt); err != nil {
		problems = append(problems, err.Error())
	}
	if c.Reports.Timezone != "Local" {
		if _, err := time.LoadLocation(c.Reports.Timezone); err != nil {
			problems = append(problems, "invalid reports.timezone")
		}
	}
	if c.Billing.Enabled && !cleanAbsolute(c.Billing.ProfilePath) {
		problems = append(problems, "billing.profile_path must be a clean absolute path when billing is enabled")
	}
	if c.Paths.SensorSocket == c.Paths.ControlSocket {
		problems = append(problems, "sensor_socket and control_socket must be different")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

func parseClock(value string) (int, int, error) {
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, errors.New("reports.daily_at must use zero-padded HH:MM")
	}
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, errors.New("reports.daily_at must use HH:MM")
	}
	hour, errH := strconv.Atoi(parts[0])
	minute, errM := strconv.Atoi(parts[1])
	if errH != nil || errM != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, errors.New("reports.daily_at must use a valid 24-hour HH:MM")
	}
	return hour, minute, nil
}

func cleanAbsolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && strings.IndexFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

func ReportLocation(c ReportsConfig) (*time.Location, error) {
	if c.Timezone == "Local" {
		return time.Local, nil
	}
	return time.LoadLocation(c.Timezone)
}

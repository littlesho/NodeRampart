// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/store"
	"golang.org/x/sys/unix"
)

type UpgradeOptions struct {
	ConfigPath    string
	BackupPath    string
	Directory     string
	TargetPackage string
}

type UpgradeCheck struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	ReasonCode string `json:"reason_code"`
	Detail     string `json:"detail"`
}

type PackageIdentity struct {
	Format       string `json:"format"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
}

type UpgradeReport struct {
	Overall            string           `json:"overall"`
	GeneratedAt        time.Time        `json:"generated_at_utc"`
	ConfigSHA256       string           `json:"config_sha256,omitempty"`
	BackupBytes        int64            `json:"backup_bytes,omitempty"`
	BackupSchema       int              `json:"backup_schema,omitempty"`
	CurrentSchema      int              `json:"current_schema"`
	RequiredFreeBytes  uint64           `json:"required_free_bytes,omitempty"`
	AvailableFreeBytes uint64           `json:"available_free_bytes,omitempty"`
	Target             *PackageIdentity `json:"target,omitempty"`
	Checks             []UpgradeCheck   `json:"checks"`
	Limitations        []string         `json:"limitations"`
}

type RestoreRehearsal struct {
	Preflight         UpgradeReport `json:"preflight"`
	Restored          bool          `json:"restored"`
	MigrationVerified bool          `json:"migration_verified"`
	SchemaVersion     int           `json:"schema_version,omitempty"`
	CleanupCompleted  bool          `json:"cleanup_completed"`
}

// UpgradePreflight reads explicit local artifacts only. It never opens the
// configured active database, changes services or runs code from a package.
func UpgradePreflight(ctx context.Context, options UpgradeOptions) (UpgradeReport, error) {
	return upgradePreflight(ctx, options, nil, nil)
}

func upgradePreflight(ctx context.Context, options UpgradeOptions, freeSpace func(*os.File) (uint64, error), runner func(context.Context, string, ...string) (string, error)) (UpgradeReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	report := UpgradeReport{Overall: "pass", GeneratedAt: time.Now().UTC(), CurrentSchema: store.SchemaVersion(), Checks: []UpgradeCheck{}, Limitations: []string{
		"Checks apply to the explicit standalone backup, not uncommitted or later active database writes.",
		"Package identity and architecture do not prove target schema support; unverified compatibility stays unknown.",
		"Secrets remain in protected files and are not included in backups or this report. No upgrade or service action is performed.",
	}}
	add := func(name, state, code, detail string) {
		report.Checks = append(report.Checks, UpgradeCheck{name, state, code, detail})
		if state == "fail" {
			report.Overall = "fail"
		} else if state == "unknown" && report.Overall == "pass" {
			report.Overall = "unknown"
		}
	}
	if err := ctx.Err(); err != nil {
		report.Overall = "unknown"
		return report, err
	}
	data, err := readFile(options.ConfigPath, maxManagedJSON, false, -1)
	var cfg config.Config
	if err == nil {
		cfg, err = decodeConfig(data)
	}
	configValid := err == nil
	if !configValid {
		add("configuration", "fail", "CONFIGURATION_UNAVAILABLE", "Configuration is invalid, unreadable or unsafe; secret values are omitted.")
	} else {
		report.ConfigSHA256 = config.Fingerprint(cfg)
		add("configuration", "pass", "CONFIGURATION_VALID", "Current binary accepts this configuration, including its schema.")
	}
	journal := filepath.Join(filepath.Dir(options.ConfigPath), ".management-apply.json")
	if _, err := readFile(journal, 2*maxManagedJSON, true, -1); errors.Is(err, os.ErrNotExist) {
		add("recovery", "pass", "NO_PENDING_RECOVERY", "No unfinished configuration apply record exists.")
	} else if err != nil {
		add("recovery", "fail", "RECOVERY_RECORD_UNREADABLE", "An unsafe or unreadable recovery record may exist; inspect it before upgrading.")
	} else {
		add("recovery", "fail", "RECOVERY_PENDING", "An unfinished configuration apply record exists; complete the existing recovery procedure first.")
	}
	if !configValid {
		add("keys", "unknown", "KEY_DEPENDENCIES_UNKNOWN", "Key dependencies cannot be determined until configuration is valid.")
	} else {
		keyErr := upgradeKeyDependencies(cfg)
		if keyErr != nil {
			add("keys", "fail", "KEY_DEPENDENCY_UNAVAILABLE", keyErr.Error())
		} else {
			add("keys", "pass", "KEY_DEPENDENCIES_AVAILABLE", "Required privacy and enabled sender credentials are safely readable; none are copied.")
		}
	}
	backup, err := store.VerifyBackup(ctx, options.BackupPath)
	if err != nil {
		if ctx.Err() != nil {
			report.Overall = "unknown"
			return report, ctx.Err()
		}
		add("backup", "fail", "BACKUP_INVALID", "Backup failed integrity, schema, foreign-key or safe-file verification; future schemas are rejected.")
	} else {
		report.BackupBytes, report.BackupSchema = backup.Bytes, backup.SchemaVersion
		add("backup", "pass", "BACKUP_VERIFIED", "Standalone backup integrity and supported historical schema are verified read-only.")
	}
	parent, err := openDirectory(options.Directory, true)
	if err != nil {
		add("disk", "fail", "ISOLATION_DIRECTORY_UNSAFE", "Choose an existing trusted private directory without symlink traversal.")
	} else {
		defer parent.Close()
		if freeSpace == nil {
			freeSpace = upgradeAvailableSpace
		}
		available, spaceErr := freeSpace(parent)
		// Allow both the restored database and migration workspace, plus the
		// configured reserve. RestoreBackup also rechecks its own actual need.
		reserve := int64(1 << 20)
		if configValid && cfg.Storage.MinFreeBytes > reserve {
			reserve = cfg.Storage.MinFreeBytes
		}
		if backup.Bytes > 0 {
			report.RequiredFreeBytes = 2*uint64(backup.Bytes) + uint64(reserve)
		}
		report.AvailableFreeBytes = available
		if spaceErr != nil {
			add("disk", "unknown", "DISK_SPACE_UNKNOWN", "Available disk space could not be inspected.")
		} else if report.RequiredFreeBytes == 0 {
			add("disk", "unknown", "DISK_NEED_UNKNOWN", "Required space cannot be estimated without a verified backup.")
		} else if available < report.RequiredFreeBytes {
			add("disk", "fail", "DISK_SPACE_LOW", "Free space is insufficient for the isolated copy, migration workspace and configured reserve.")
		} else {
			add("disk", "pass", "DISK_SPACE_AVAILABLE", "Free space exceeds the conservative rehearsal estimate; storage can still change before execution.")
		}
	}
	if options.TargetPackage == "" {
		add("target_package", "unknown", "TARGET_PACKAGE_NOT_PROVIDED", "No target package was supplied; target identity and compatibility are unknown.")
	} else {
		identity, state, code := inspectTargetPackage(ctx, options.TargetPackage, runner)
		if ctx.Err() != nil {
			report.Overall = "unknown"
			return report, ctx.Err()
		}
		report.Target = identity
		add("target_package", state, code, "Only local package metadata and native architecture are inspected; package programs are never executed.")
	}
	add("target_compatibility", "unknown", "TARGET_SCHEMA_SUPPORT_UNKNOWN", "A package version is not proof of its supported schema/configuration. Check verified release documentation before upgrading.")
	return report, nil
}

func upgradeAvailableSpace(parent *os.File) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(parent.Fd()), &stat); err != nil || stat.Bsize <= 0 {
		return 0, errors.New("disk space unavailable")
	}
	if stat.Bavail > ^uint64(0)/uint64(stat.Bsize) {
		return 0, errors.New("disk space exceeds supported range")
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

func upgradeKeyDependencies(cfg config.Config) error {
	if cfg.Privacy.StoreIP == "hash" || cfg.Privacy.NotificationIP == "hash" {
		data, err := readFile(cfg.Privacy.HashKeyFile, 4096, true, -1)
		if err != nil || len(strings.TrimSpace(string(data))) < 32 {
			return errors.New("Required hash privacy key is missing, unsafe or invalid; plaintext fallback is forbidden.")
		}
	}
	if cfg.Notifications.Telegram.Enabled {
		_, err := readFile(cfg.Notifications.Telegram.TokenFile, 4096, true, -1)
		if err != nil {
			return errors.New("Enabled Telegram credentials are missing or unsafe; they are not included in backups.")
		}
		if _, err := notify.TelegramDestination(cfg.Notifications.Telegram.TokenFile, cfg.Notifications.Telegram.ChatID); err != nil {
			return errors.New("Enabled Telegram credentials or destination identity are invalid.")
		}
	}
	if cfg.Notifications.Webhook.Enabled {
		if _, err := readFile(cfg.Notifications.Webhook.CredentialFile, 512, true, -1); err != nil {
			return errors.New("Enabled Webhook credentials are missing or unsafe; they are not included in backups.")
		}
		if _, err := notify.NewWebhook(cfg.Notifications.Webhook.Endpoint, cfg.Notifications.Webhook.ReceiverID, cfg.Notifications.Webhook.CredentialFile, cfg.Notifications.Webhook.Timeout.Duration); err != nil {
			return errors.New("Enabled Webhook credentials or fixed target are invalid.")
		}
	}
	for _, channel := range config.NativeChannelNames() {
		n := cfg.Notifications.NativeChannels()[channel]
		if !n.Enabled {
			continue
		}
		if _, err := notify.NewNative(channel, n); err != nil {
			return errors.New(channel + " credentials are missing, unsafe or invalid; credentials are not included in backups / 凭据缺失、不安全或无效；备份不包含凭据")
		}
	}
	for _, channel := range config.OfficialChannelNames() {
		c := cfg.Notifications.OfficialChannels()[channel]
		if !c.Enabled {
			continue
		}
		if _, err := notify.NewOfficial(channel, c); err != nil {
			return errors.New(channel + " credentials are missing, unsafe or invalid; credentials are not included in backups / 凭据缺失、不安全或无效；备份不包含凭据")
		}
	}
	if cfg.Heartbeat.Enabled {
		if _, err := readFile(cfg.Heartbeat.CredentialFile, 512, true, -1); err != nil {
			return errors.New("Enabled heartbeat credentials are missing or unsafe; they are not included in backups.")
		}
		if _, err := notify.NewHeartbeat(cfg.Heartbeat.Endpoint, cfg.Heartbeat.CredentialFile, cfg.Heartbeat.Timeout.Duration); err != nil {
			return errors.New("Enabled heartbeat credentials or fixed target are invalid.")
		}
	}
	return nil
}

var packageMetadataField = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+:~_-]{0,127}$`)

func inspectTargetPackage(ctx context.Context, path string, runner func(context.Context, string, ...string) (string, error)) (*PackageIdentity, string, string) {
	file, before, err := openManagedFile(path, 512<<20, false, -1)
	if err != nil {
		return nil, "unknown", "TARGET_PACKAGE_UNAVAILABLE"
	}
	defer file.Close()
	pinned := fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), file.Fd())
	var tool string
	var args []string
	identity := &PackageIdentity{}
	switch filepath.Ext(path) {
	case ".deb":
		identity.Format, tool = "deb", "/usr/bin/dpkg-deb"
		args = []string{"--field", pinned, "Package", "Version", "Architecture"}
	case ".rpm":
		identity.Format, tool = "rpm", "/usr/bin/rpm"
		args = []string{"--query", "--package", "--queryformat", "%{NAME}\n%{VERSION}-%{RELEASE}\n%{ARCH}\n", pinned}
	default:
		return nil, "unknown", "TARGET_PACKAGE_FORMAT_UNKNOWN"
	}
	work, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	manager := &Manager{runner: runner}
	output, err := manager.command(work, tool, args...)
	if err != nil || len(output) > 4096 {
		return nil, "unknown", "TARGET_METADATA_UNAVAILABLE"
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 {
		return nil, "unknown", "TARGET_METADATA_INVALID"
	}
	for i, line := range lines {
		if identity.Format == "deb" {
			prefixes := []string{"Package: ", "Version: ", "Architecture: "}
			if !strings.HasPrefix(line, prefixes[i]) {
				return nil, "unknown", "TARGET_METADATA_INVALID"
			}
			lines[i] = strings.TrimPrefix(line, prefixes[i])
		}
		if !packageMetadataField.MatchString(lines[i]) {
			return nil, "unknown", "TARGET_METADATA_INVALID"
		}
	}
	identity.Name, identity.Version, identity.Architecture = lines[0], lines[1], lines[2]
	var after unix.Stat_t
	if unix.Fstat(int(file.Fd()), &after) != nil || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return nil, "unknown", "TARGET_PACKAGE_CHANGED"
	}
	if identity.Name != "noderampart" {
		return identity, "fail", "TARGET_PACKAGE_IDENTITY_MISMATCH"
	}
	architecture := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if identity.Format == "rpm" {
		architecture = map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	}
	if architecture == "" {
		return identity, "unknown", "NATIVE_ARCHITECTURE_UNKNOWN"
	}
	if identity.Architecture != architecture {
		return identity, "fail", "TARGET_ARCHITECTURE_MISMATCH"
	}
	return identity, "pass", "TARGET_METADATA_VERIFIED"
}

// RehearseRestore migrates only a new private copy with the current binary and
// deletes that owned directory afterward. It never copies credentials, changes
// active services or repairs an active database.
func RehearseRestore(ctx context.Context, options UpgradeOptions) (result RestoreRehearsal, err error) {
	result.Preflight, err = UpgradePreflight(ctx, options)
	if err != nil {
		return result, err
	}
	for _, check := range result.Preflight.Checks {
		if check.Name == "configuration" || check.Name == "recovery" || check.Name == "keys" || check.Name == "backup" || check.Name == "disk" {
			if check.State != "pass" {
				return result, errors.New("restore rehearsal requires verified configuration, dependencies, backup, recovery state and disk space")
			}
		}
	}
	parent, err := openDirectory(options.Directory, true)
	if err != nil {
		return result, err
	}
	defer parent.Close()
	name := ".noderampart-rehearsal-" + rand.Text()
	if err := unix.Mkdirat(int(parent.Fd()), name, 0o700); err != nil {
		return result, errors.New("private rehearsal directory could not be created")
	}
	// Store restore APIs independently pin/validate paths. Keeping the trusted
	// parent descriptor prevents cleanup from following a changed pathname.
	directory, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
		return result, errors.New("private rehearsal directory is unavailable")
	}
	dir := os.NewFile(uintptr(directory), "restore-rehearsal")
	defer dir.Close()
	defer func() {
		cleanupErr := cleanupRehearsal(parent, dir, name)
		result.CleanupCompleted = cleanupErr == nil
		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	data, err := readFile(options.ConfigPath, maxManagedJSON, false, -1)
	if err != nil {
		return result, err
	}
	cfg, err := decodeConfig(data)
	if err != nil || config.Fingerprint(cfg) != result.Preflight.ConfigSHA256 {
		return result, errors.New("configuration changed after preflight")
	}
	target := filepath.Join(options.Directory, name, "restored.db")
	if _, err := store.RestoreBackup(ctx, options.BackupPath, target); err != nil {
		return result, errors.New("isolated restore failed")
	}
	result.Restored = true
	if err := ctx.Err(); err != nil {
		return result, err
	}
	copy, err := store.OpenWithBudget(target, store.BudgetConfig{MaxBytes: cfg.Storage.MaxBytes, MinFreeBytes: cfg.Storage.MinFreeBytes})
	if err != nil {
		return result, errors.New("isolated database schema migration failed")
	}
	if err := copy.Close(); err != nil {
		return result, errors.New("isolated database close failed")
	}
	info, err := store.VerifyBackup(ctx, target)
	if err != nil {
		return result, errors.New("migrated restore copy verification failed")
	}
	result.MigrationVerified, result.SchemaVersion = true, info.SchemaVersion
	return result, nil
}

func cleanupRehearsal(parent, dir *os.File, name string) error {
	// No recursive removal. Only fixed SQLite outputs owned by this rehearsal
	// are eligible; an unexpected entry preserves evidence and fails cleanup.
	var opened, named unix.Stat_t
	if unix.Fstat(int(dir.Fd()), &opened) != nil || unix.Fstatat(int(parent.Fd()), name, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || opened.Dev != named.Dev || opened.Ino != named.Ino {
		return errors.New("rehearsal directory changed; cleanup preserved evidence")
	}
	entries, err := dir.ReadDir(5)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > 4 {
		return errors.New("rehearsal cleanup could not inspect its bounded private directory")
	}
	for _, entry := range entries {
		if entry.Name() != "restored.db" && entry.Name() != "restored.db-journal" && entry.Name() != "restored.db-wal" && entry.Name() != "restored.db-shm" {
			return errors.New("rehearsal directory contains unexpected evidence; inspect and clean it manually")
		}
		var stat unix.Stat_t
		if unix.Fstatat(int(dir.Fd()), entry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
			return errors.New("rehearsal cleanup refused an unsafe file")
		}
	}
	for _, entry := range entries {
		if unix.Unlinkat(int(dir.Fd()), entry.Name(), 0) != nil {
			return errors.New("rehearsal temporary file cleanup failed")
		}
	}
	if dir.Sync() != nil || unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR) != nil || parent.Sync() != nil {
		return errors.New("rehearsal directory cleanup failed")
	}
	return nil
}

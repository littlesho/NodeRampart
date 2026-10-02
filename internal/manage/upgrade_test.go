// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func upgradeFixture(t *testing.T) (UpgradeOptions, config.Config) {
	t.Helper()
	dir := t.TempDir()
	options := UpgradeOptions{ConfigPath: filepath.Join(dir, "config.json"), BackupPath: filepath.Join(dir, "snapshot.db"), Directory: dir}
	cfg := config.Defaults()
	cfg.Paths.Database = filepath.Join(dir, "active.db")
	cfg.Storage.MinFreeBytes = 0
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(options.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.Paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertEvent(context.Background(), model.Event{ID: "retained-event", Kind: "test", ObservedAt: time.Now().UTC(), Severity: model.SeverityInfo}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Backup(context.Background(), options.BackupPath); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return options, cfg
}

func upgradeCheck(t *testing.T, report UpgradeReport, name string) UpgradeCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("missing check %s", name)
	return UpgradeCheck{}
}

func TestUpgradePreflightIsReadOnlyAndTargetCompatibilityUnknown(t *testing.T) {
	options, cfg := upgradeFixture(t)
	before, err := os.ReadFile(cfg.Paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	backupBefore, _ := os.ReadFile(options.BackupPath)
	report, err := UpgradePreflight(context.Background(), options)
	if err != nil || report.Overall != "unknown" || report.BackupSchema != store.SchemaVersion() {
		t.Fatalf("preflight=%+v %v", report, err)
	}
	for _, name := range []string{"configuration", "keys", "backup", "disk", "recovery"} {
		if upgradeCheck(t, report, name).State != "pass" {
			t.Fatalf("%s failed: %+v", name, report)
		}
	}
	if upgradeCheck(t, report, "target_compatibility").State != "unknown" {
		t.Fatal("guessed schema compatibility")
	}
	after, _ := os.ReadFile(cfg.Paths.Database)
	backupAfter, _ := os.ReadFile(options.BackupPath)
	if !bytes.Equal(before, after) || !bytes.Equal(backupBefore, backupAfter) {
		t.Fatal("read-only preflight changed input databases")
	}
	for _, sidecar := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(options.BackupPath + sidecar); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("preflight left backup sidecar")
		}
	}
}

func TestUpgradePreflightReportsDependenciesRecoverySpaceAndFutureSchema(t *testing.T) {
	options, cfg := upgradeFixture(t)
	cfg.Privacy.StoreIP, cfg.Privacy.HashKeyFile = "hash", filepath.Join(options.Directory, "privacy.key")
	cfg.Notifications.Telegram.Enabled, cfg.Notifications.Telegram.ChatID = true, "-12345"
	cfg.Notifications.Telegram.TokenFile = filepath.Join(options.Directory, "telegram.token")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(options.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.Directory, ".management-apply.json"), []byte(`{"version":2,"stage":"activating"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := upgradePreflight(context.Background(), options, func(*os.File) (uint64, error) { return 1, nil }, nil)
	if err != nil || report.Overall != "fail" {
		t.Fatalf("preflight=%+v %v", report, err)
	}
	for _, name := range []string{"keys", "disk", "recovery"} {
		if upgradeCheck(t, report, name).State != "fail" {
			t.Fatalf("accepted %s", name)
		}
	}
	secret := "synthetic-only-privacy-secret-1234567890"
	if err := os.WriteFile(cfg.Privacy.HashKeyFile, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Notifications.Telegram.TokenFile, []byte("123456:syntheticOnly_Token-abcdefghijklmno"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(options.Directory, ".management-apply.json")); err != nil {
		t.Fatal(err)
	}
	report, err = UpgradePreflight(context.Background(), options)
	if err != nil || upgradeCheck(t, report, "keys").State != "pass" {
		t.Fatalf("safe dependencies rejected=%+v %v", report, err)
	}
	encoded, _ := json.Marshal(report)
	if bytes.Contains(encoded, []byte(secret)) || bytes.Contains(encoded, []byte("syntheticOnly_Token")) || bytes.Contains(encoded, []byte(cfg.Privacy.HashKeyFile)) {
		t.Fatal("preflight disclosed secret or secret path")
	}
	db, err := sql.Open("sqlite", options.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,1)`, store.SchemaVersion()+1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report, err = UpgradePreflight(context.Background(), options)
	if err != nil || upgradeCheck(t, report, "backup").State != "fail" {
		t.Fatalf("future schema accepted: %+v %v", report, err)
	}
	if _, err := RehearseRestore(context.Background(), options); err == nil {
		t.Fatal("rehearsal accepted future schema")
	}
}

func TestRestoreRehearsalMigratesHistoricalCopyAndCleansOnlyOwnedOutputs(t *testing.T) {
	options, cfg := upgradeFixture(t)
	// A schema-7 fixture preserves event history and predates target isolation,
	// complete local reports and commit watermarks. Production migrations must
	// upgrade this private copy, while the supplied snapshot remains immutable.
	db, err := sql.Open("sqlite", options.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	// A real schema-7 fixture has none of the schema-14 dispatch or budget tables.
	// Fixture construction is not a production downgrade facility.
	for _, query := range []string{`DROP TABLE notification_dispatch`, `DROP TABLE official_budget_usage`, `DROP TABLE official_channel_policy`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{`ALTER TABLE notification_outbox DROP COLUMN language`, `ALTER TABLE notification_outbox DROP COLUMN presentation_timezone`, `DROP INDEX event_notifications_message_idx`, `DROP TABLE event_notifications`, `CREATE TABLE event_notifications (event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE, notification_id TEXT NOT NULL DEFAULT '', decision TEXT NOT NULL CHECK(decision IN ('queued','merged','silenced','ineligible','rejected')), silence_id TEXT NOT NULL DEFAULT '', recorded_at INTEGER NOT NULL)`, `CREATE INDEX event_notifications_message_idx ON event_notifications(notification_id)`, `DROP TABLE sensor_watermarks`, `DROP TABLE sensor_commit_state`, `ALTER TABLE report_snapshots DROP COLUMN document_json`, `DROP TABLE notification_targets`, `ALTER TABLE notification_outbox DROP COLUMN channel`, `ALTER TABLE notification_outbox DROP COLUMN isolated_at`, `DELETE FROM schema_migrations WHERE version>7`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifyBackup(context.Background(), options.BackupPath); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(options.BackupPath)
	active, _ := os.ReadFile(cfg.Paths.Database)
	result, err := RehearseRestore(context.Background(), options)
	if err != nil || !result.Restored || !result.MigrationVerified || !result.CleanupCompleted || result.SchemaVersion != store.SchemaVersion() || result.Preflight.BackupSchema != 7 {
		t.Fatalf("rehearsal=%+v %v", result, err)
	}
	after, _ := os.ReadFile(options.BackupPath)
	activeAfter, _ := os.ReadFile(cfg.Paths.Database)
	if !bytes.Equal(before, after) || !bytes.Equal(active, activeAfter) {
		t.Fatal("rehearsal changed original data")
	}
	entries, _ := os.ReadDir(options.Directory)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".noderampart-rehearsal-") {
			t.Fatal("rehearsal temporary directory leaked")
		}
	}
}

func TestUpgradeRejectsUnsafePathsAndCancelledWork(t *testing.T) {
	options, _ := upgradeFixture(t)
	link := filepath.Join(options.Directory, "linked-config")
	if err := os.Symlink(options.ConfigPath, link); err != nil {
		t.Fatal(err)
	}
	unsafe := options
	unsafe.ConfigPath = link
	report, err := UpgradePreflight(context.Background(), unsafe)
	if err != nil || upgradeCheck(t, report, "configuration").State != "fail" {
		t.Fatal("accepted linked configuration")
	}
	unsafe = options
	unsafe.Directory = filepath.Join(options.Directory, "linked-dir")
	if err := os.Symlink(options.Directory, unsafe.Directory); err != nil {
		t.Fatal(err)
	}
	if _, err := RehearseRestore(context.Background(), unsafe); err == nil {
		t.Fatal("accepted linked isolation directory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RehearseRestore(ctx, options); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
	parent, err := openDirectory(options.Directory, true)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	name := ".noderampart-rehearsal-unexpected-fixture"
	dirpath := filepath.Join(options.Directory, name)
	if err := os.Mkdir(dirpath, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := openDirectory(dirpath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := os.WriteFile(filepath.Join(dirpath, "keep-evidence"), []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupRehearsal(parent, dir, name); err == nil {
		t.Fatal("cleanup deleted unknown evidence")
	}
	if _, err := os.Stat(filepath.Join(dirpath, "keep-evidence")); err != nil {
		t.Fatal("unknown evidence was removed")
	}
}

func TestUpgradeChecksEnabledHTTPSCredentialDependenciesWithoutSending(t *testing.T) {
	options, cfg := upgradeFixture(t)
	credential := filepath.Join(options.Directory, "https.credential")
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Endpoint = "https://example.invalid/fixed-heartbeat"
	cfg.Heartbeat.InstanceID = "fixture-instance"
	cfg.Heartbeat.CredentialFile = credential
	cfg.Notifications.Webhook.Enabled = true
	cfg.Notifications.Webhook.Endpoint = "https://example.invalid/fixed-webhook"
	cfg.Notifications.Webhook.ReceiverID = "fixture-receiver"
	cfg.Notifications.Webhook.CredentialFile = credential
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(options.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := UpgradePreflight(context.Background(), options)
	if err != nil || upgradeCheck(t, report, "keys").State != "fail" {
		t.Fatalf("missing HTTPS credentials accepted: %+v %v", report, err)
	}
	secret := "synthetic-only-protected-bearer"
	if err := os.WriteFile(credential, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = UpgradePreflight(context.Background(), options)
	if err != nil || upgradeCheck(t, report, "keys").State != "pass" {
		t.Fatalf("protected HTTPS credentials rejected: %+v %v", report, err)
	}
	encoded, _ := json.Marshal(report)
	if bytes.Contains(encoded, []byte(secret)) || bytes.Contains(encoded, []byte("example.invalid")) || bytes.Contains(encoded, []byte(credential)) {
		t.Fatal("preflight exported HTTPS target/secret")
	}
}

func TestTargetPackageMetadataNeverRunsPackagePrograms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.deb")
	if err := os.WriteFile(path, []byte("synthetic-package"), 0o600); err != nil {
		t.Fatal(err)
	}
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" {
		t.Skip("native package architecture is unsupported")
	}
	for _, tc := range []struct{ name, packageName, packageArch, want string }{{"valid", "noderampart", arch, "pass"}, {"wrong name", "other", arch, "fail"}, {"wrong arch", "noderampart", "wrong", "fail"}} {
		t.Run(tc.name, func(t *testing.T) {
			runner := func(_ context.Context, program string, args ...string) (string, error) {
				if program != "/usr/bin/dpkg-deb" || len(args) != 5 || args[0] != "--field" || !strings.HasPrefix(args[1], "/proc/") || args[1] == path {
					t.Fatalf("unexpected execution %s %v", program, args)
				}
				return "Package: " + tc.packageName + "\nVersion: 0.4.0~alpha.5\nArchitecture: " + tc.packageArch + "\n", nil
			}
			identity, state, _ := inspectTargetPackage(context.Background(), path, runner)
			if identity == nil || state != tc.want {
				t.Fatalf("identity=%+v state=%s", identity, state)
			}
		})
	}
	for _, output := range []string{"malformed", strings.Repeat("a", 4097), "Package: noderampart\nVersion: injected\nArchitecture: " + arch + "\nextra"} {
		if _, state, _ := inspectTargetPackage(context.Background(), path, func(context.Context, string, ...string) (string, error) { return output, nil }); state != "unknown" {
			t.Fatal("unverified metadata became compatible")
		}
	}
	if _, state, _ := inspectTargetPackage(context.Background(), path, func(context.Context, string, ...string) (string, error) { return "", errors.New("tool unavailable") }); state != "unknown" {
		t.Fatal("missing tool became known")
	}
}

func TestActualDebMetadataIsInspectedWithoutMaintainerScripts(t *testing.T) {
	if _, err := os.Stat("/usr/bin/dpkg-deb"); err != nil {
		t.Skip("dpkg-deb is unavailable")
	}
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" {
		t.Skip("native DEB architecture unsupported")
	}
	dir := t.TempDir()
	tree := filepath.Join(dir, "package")
	control := filepath.Join(tree, "DEBIAN")
	if err := os.MkdirAll(control, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "control"), []byte("Package: noderampart\nVersion: 0.4.0~alpha.5\nArchitecture: "+arch+"\nMaintainer: Fixture <fixture@example.invalid>\nDescription: isolated metadata fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "must-not-execute")
	if err := os.WriteFile(filepath.Join(control, "preinst"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "target.deb")
	if output, err := exec.Command("/usr/bin/dpkg-deb", "--build", "--root-owner-group", tree, path).CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, output)
	}
	identity, state, code := inspectTargetPackage(context.Background(), path, nil)
	if state != "pass" || identity == nil {
		t.Fatalf("identity=%+v state=%s code=%s", identity, state, code)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("metadata inspection executed package script")
	}
}

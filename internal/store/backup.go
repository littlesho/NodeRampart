// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type BackupInfo struct {
	Path          string    `json:"path"`
	Bytes         int64     `json:"bytes"`
	SchemaVersion int       `json:"schema_version"`
	CreatedAt     time.Time `json:"created_at_utc"`
}

// Backup creates a consistent SQLite snapshot, validates it, then atomically
// publishes a new 0600 file. The caller chooses an authorized output directory.
// The output and every parent must be real paths, not symlinks. Existing files
// are never overwritten. VACUUM INTO includes committed WAL contents as well.
func (s *Store) Backup(ctx context.Context, target string) (BackupInfo, error) {
	if err := s.lockBudget(ctx); err != nil {
		return BackupInfo{}, err
	}
	defer s.budgetMu.Unlock()
	minimumFree := int64(0)
	if s.budget != nil {
		minimumFree = s.budget.config.MinFreeBytes
	}
	return snapshotInto(ctx, s.db, target, minimumFree)
}

// VerifyBackup opens a standalone snapshot read-only, checks SQLite integrity
// and the supported NodeRampart schema, and does not migrate or alter the file.
func VerifyBackup(ctx context.Context, path string) (BackupInfo, error) {
	parent, base, err := secureParent(path)
	if err != nil {
		return BackupInfo{}, err
	}
	defer parent.Close()
	file, err := openSnapshot(parent, base)
	if err != nil {
		return BackupInfo{}, err
	}
	defer file.Close()
	info, err := verifySnapshot(ctx, fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
	info.Path = path
	return info, err
}

// RestoreBackup reconstructs a verified standalone snapshot at a NEW database
// path. It never replaces an existing database or sidecar. Stop the daemon
// before selecting the restored path in configuration. Active WAL/journal
// sources are refused; SQLite's read lock also refuses an exclusively owned DB.
func RestoreBackup(ctx context.Context, source, target string) (BackupInfo, error) {
	parent, base, err := secureParent(source)
	if err != nil {
		return BackupInfo{}, err
	}
	defer parent.Close()
	file, err := openSnapshot(parent, base)
	if err != nil {
		return BackupInfo{}, err
	}
	defer file.Close()
	path := fmt.Sprintf("/proc/self/fd/%d", file.Fd())
	if _, err := verifySnapshot(ctx, path); err != nil {
		return BackupInfo{}, err
	}
	db, err := openReadOnlySnapshot(path)
	if err != nil {
		return BackupInfo{}, err
	}
	defer db.Close()
	return snapshotInto(ctx, db, target, 0)
}

func openReadOnlySnapshot(path string) (*sql.DB, error) {
	uri := (&url.URL{Scheme: "file", Path: path}).String() + "?mode=ro"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func verifySnapshot(ctx context.Context, path string) (BackupInfo, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return BackupInfo{}, err
	}
	if !fileInfo.Mode().IsRegular() || fileInfo.Size() == 0 || fileInfo.Size() > 1<<40 {
		return BackupInfo{}, errors.New("backup must be a nonempty regular SQLite file at most 1 TiB")
	}
	db, err := openReadOnlySnapshot(path)
	if err != nil {
		return BackupInfo{}, err
	}
	defer db.Close()
	for _, statement := range []string{`PRAGMA busy_timeout=100`, `PRAGMA trusted_schema=OFF`, `PRAGMA query_only=ON`} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return BackupInfo{}, fmt.Errorf("configure backup verification: %w", err)
		}
	}
	version, err := verifySnapshotSchema(ctx, db)
	if err != nil {
		return BackupInfo{}, err
	}
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return BackupInfo{}, fmt.Errorf("read backup integrity: %w", err)
	}
	valid := false
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			rows.Close()
			return BackupInfo{}, err
		}
		if result != "ok" {
			rows.Close()
			return BackupInfo{}, errors.New("backup integrity check failed")
		}
		valid = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return BackupInfo{}, err
	}
	if err := rows.Close(); err != nil {
		return BackupInfo{}, err
	}
	if !valid {
		return BackupInfo{}, errors.New("backup integrity check produced no result")
	}
	return BackupInfo{Path: path, Bytes: fileInfo.Size(), SchemaVersion: version, CreatedAt: fileInfo.ModTime().UTC()}, nil
}

func verifySnapshotSchema(ctx context.Context, db *sql.DB) (int, error) {
	reference, err := newSnapshotSchemaReference(ctx)
	if err != nil {
		return 0, fmt.Errorf("construct expected backup schema: %w", err)
	}
	defer reference.Close()
	actualMigrations, err := readSnapshotTable(ctx, db, "schema_migrations")
	if err != nil {
		return 0, err
	}
	expectedMigrations, err := readSnapshotTable(ctx, reference, "schema_migrations")
	if err != nil {
		return 0, err
	}
	if err := compareSnapshotTable(actualMigrations, expectedMigrations, "version applied_at", "schema_migrations"); err != nil {
		return 0, err
	}

	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return 0, errors.New("backup has no valid NodeRampart schema version")
	}
	version := 0
	for rows.Next() {
		var next int
		if err := rows.Scan(&next); err != nil {
			rows.Close()
			return 0, errors.New("backup migration chain is invalid")
		}
		if next != version+1 || next > schemaVersion {
			rows.Close()
			return 0, errors.New("backup migration chain is missing or unsupported")
		}
		version = next
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if version == 0 {
		return 0, errors.New("backup has no schema migration records")
	}
	var executableObjects int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type IN ('view','trigger') OR UPPER(sql) LIKE '%CREATE VIRTUAL TABLE%'`).Scan(&executableObjects); err != nil {
		return 0, err
	}
	if executableObjects != 0 {
		return 0, errors.New("backup contains unsupported executable schema objects")
	}
	tables := map[string]string{
		"schema_migrations":          "version applied_at",
		"events":                     "id incident_id observed_at kind phase severity summary source_ip source_range target count geo_json evidence_json",
		"traffic_hourly":             "hour_utc direction country region asn asn_org attributed bytes packets",
		"auth_hourly":                "hour_utc kind source_range count",
		"auth_cardinality_hourly":    "hour_utc keys",
		"traffic_cardinality_hourly": "hour_utc keys",
		"interface_hourly":           "hour_utc rx_bytes tx_bytes rx_packets tx_packets",
		"collector_health_hourly":    "hour_utc batches overflow_bytes overflow_packets parse_errors kernel_packets kernel_drops kernel_stats_errors last_seen",
		"notification_outbox":        "id dedupe_key destination body attempts next_attempt last_error created_at sent_at",
		"report_runs":                "report_date destination generated_at",
		"component_status":           "name state updated_at",
	}
	if version >= 2 {
		tables["collector_health_hourly"] += " ipc_dropped_batches ipc_dropped_packets ipc_dropped_bytes health_counter_saturations"
	}
	if version >= 3 {
		tables["notification_outbox"] += " quarantined_at expires_at"
		tables["notification_cooldowns"] = "destination until_at"
		tables["notification_counters"] = "id rejected expired last_sent_at"
		tables["report_snapshots"] = "report_date title body period_start period_end generated_at"
		tables["coverage_intervals"] = "id name state started_at ended_at"
		tables["coverage_gaps"] = "id name reason started_at ended_at count"
		tables["collector_checkpoints"] = "name cursor observed_at updated_at"
		tables["journal_seen"] = "cursor_hash received_at"
	}
	if version >= 4 {
		tables["notification_outbox"] += " incident_id event_kind event_phase merged_count merge_until suppressed_at lease_until"
		tables["event_notifications"] = "event_id notification_id decision silence_id recorded_at"
		tables["notification_silences"] = "id incident_id kind reason created_at expires_at revoked_at"
		tables["interface_detail_hourly"] = "hour_utc interface rx_bytes tx_bytes rx_packets tx_packets"
	}
	if version >= 5 {
		tables["report_snapshots"] += " billing_json"
	}
	if version >= 6 {
		tables["monitor_state"] = "key revision updated_at data"
		tables["retention_meta"] = "id tracking_started evicted_entries"
		tables["retention_ledger"] = "id dataset reason action_day operations affected_rows data_start data_end first_action last_action"
		tables["retention_totals"] = "dataset reason operations affected_rows data_start data_end first_action last_action"
	}
	rows, err = db.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table'`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return 0, err
		}
		if _, known := tables[table]; !known {
			rows.Close()
			return 0, errors.New("backup contains unsupported additional tables")
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := downgradeSnapshotSchemaReference(ctx, reference, version); err != nil {
		return 0, err
	}
	for table, columnNames := range tables {
		actual, err := readSnapshotTable(ctx, db, table)
		if err != nil {
			return 0, err
		}
		wanted, err := readSnapshotTable(ctx, reference, table)
		if err != nil {
			return 0, err
		}
		if err := compareSnapshotTable(actual, wanted, columnNames, table); err != nil {
			return 0, err
		}
	}
	return version, nil
}

func snapshotInto(ctx context.Context, db *sql.DB, target string, minimumFree int64) (BackupInfo, error) {
	output, err := createSnapshotTarget(target)
	if err != nil {
		return BackupInfo{}, err
	}
	defer output.close()
	var pages, pageSize int64
	if err := db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return BackupInfo{}, err
	}
	if err := db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return BackupInfo{}, err
	}
	available, err := availableDiskBytes(fmt.Sprintf("/proc/self/fd/%d", output.parent.Fd()))
	if err != nil {
		return BackupInfo{}, err
	}
	if pages < 0 || pageSize < 512 || pageSize > 65536 || pages > (1<<40)/pageSize {
		return BackupInfo{}, errors.New("backup source exceeds supported size")
	}
	if available < uint64(pages*pageSize+minimumFree+budgetFileOverhead) {
		return BackupInfo{}, errors.New("insufficient free space for consistent backup")
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, output.sqlitePath()); err != nil {
		return BackupInfo{}, fmt.Errorf("create consistent backup: %w", err)
	}
	info, err := verifySnapshot(ctx, output.sqlitePath())
	if err != nil {
		return BackupInfo{}, err
	}
	file, err := os.OpenFile(output.sqlitePath(), os.O_RDWR, 0)
	if err != nil {
		return BackupInfo{}, err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return BackupInfo{}, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return BackupInfo{}, err
	}
	if err := file.Close(); err != nil {
		return BackupInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return BackupInfo{}, err
	}
	if err := output.publish(); err != nil {
		return BackupInfo{}, err
	}
	info.Path = target
	return info, nil
}

type snapshotTarget struct {
	parent, temporary *os.File
	base, directory   string
}

func createSnapshotTarget(target string) (*snapshotTarget, error) {
	if !filepath.IsAbs(target) || filepath.Clean(target) != target || target == "/" || strings.IndexByte(target, 0) >= 0 {
		return nil, errors.New("backup path must be a clean absolute file path")
	}
	parent, base, err := secureParent(target)
	if err != nil {
		// Create only one missing parent, through an already validated directory
		// descriptor. This supports the daemon's private state/backups directory
		// without following a symlink or creating an arbitrary missing tree.
		grandparent, name, parentErr := secureParent(filepath.Dir(target))
		if parentErr != nil {
			return nil, err
		}
		parentErr = unix.Mkdirat(int(grandparent.Fd()), name, 0o700)
		_ = grandparent.Close()
		if parentErr != nil && !errors.Is(parentErr, unix.EEXIST) {
			return nil, parentErr
		}
		parent, base, err = secureParent(target)
		if err != nil {
			return nil, err
		}
	}
	output := &snapshotTarget{parent: parent, base: base}
	if err := ensureAbsent(parent, base); err != nil {
		parent.Close()
		return nil, err
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		parent.Close()
		return nil, err
	}
	output.directory = ".noderampart-backup-" + hex.EncodeToString(random[:])
	if err := unix.Mkdirat(int(parent.Fd()), output.directory, 0o700); err != nil {
		parent.Close()
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), output.directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		output.close()
		return nil, err
	}
	output.temporary = os.NewFile(uintptr(fd), output.directory)
	fd, err = unix.Openat(fd, "snapshot.db", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		output.close()
		return nil, err
	}
	if err := unix.Close(fd); err != nil {
		output.close()
		return nil, err
	}
	return output, nil
}

func (o *snapshotTarget) sqlitePath() string {
	return fmt.Sprintf("/proc/self/fd/%d/snapshot.db", o.temporary.Fd())
}
func (o *snapshotTarget) publish() error {
	if err := ensureAbsent(o.parent, o.base); err != nil {
		return err
	}
	// linkat fails atomically if the destination exists, including symlinks.
	if err := unix.Linkat(int(o.temporary.Fd()), "snapshot.db", int(o.parent.Fd()), o.base, 0); err != nil {
		return fmt.Errorf("publish new backup without overwrite: %w", err)
	}
	if err := o.parent.Sync(); err != nil {
		return fmt.Errorf("sync backup directory: %w", err)
	}
	return nil
}
func (o *snapshotTarget) close() {
	if o.temporary != nil {
		for _, name := range []string{"snapshot.db", "snapshot.db-wal", "snapshot.db-shm", "snapshot.db-journal"} {
			_ = unix.Unlinkat(int(o.temporary.Fd()), name, 0)
		}
		_ = o.temporary.Close()
	}
	if o.parent != nil {
		if o.directory != "" {
			_ = unix.Unlinkat(int(o.parent.Fd()), o.directory, unix.AT_REMOVEDIR)
		}
		_ = o.parent.Close()
	}
}

func secureParent(path string) (*os.File, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.IndexByte(path, 0) >= 0 {
		return nil, "", errors.New("backup path must be a clean absolute file path")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	for _, component := range strings.Split(strings.TrimPrefix(filepath.Dir(path), "/"), "/") {
		if component == "" {
			continue
		}
		next, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, "", errors.New("backup parent must exist and contain no symlinks")
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), filepath.Dir(path)), filepath.Base(path), nil
}

func ensureAbsent(parent *os.File, base string) error {
	for _, name := range []string{base, base + "-wal", base + "-shm", base + "-journal"} {
		var info unix.Stat_t
		err := unix.Fstatat(int(parent.Fd()), name, &info, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil {
			return errors.New("backup destination or sidecar already exists")
		}
		if !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	return nil
}

func openSnapshot(parent *os.File, base string) (*os.File, error) {
	for _, name := range []string{base + "-wal", base + "-shm", base + "-journal"} {
		var info unix.Stat_t
		err := unix.Fstatat(int(parent.Fd()), name, &info, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil {
			return nil, errors.New("backup source has active SQLite sidecars; use a standalone snapshot")
		}
		if !errors.Is(err, unix.ENOENT) {
			return nil, err
		}
	}
	fd, err := unix.Openat(int(parent.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), base)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("backup source must be a regular file")
	}
	return file, nil
}

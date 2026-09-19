// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
	_ "modernc.org/sqlite"
)

type Store struct {
	db          *sql.DB
	path        string
	budgetMu    sync.Mutex
	budget      *budgetState
	mergeWindow time.Duration
}

const (
	maxPendingOutboxMessages = 10_000
	maxPendingOutboxBytes    = 32 << 20
	maxAuthKeysPerHour       = 65_536
	maxTrafficKeysPerHour    = 16_384
)

func Open(path string) (*Store, error) {
	return openStore(path, nil)
}

// OpenWithBudget enforces the configured page/journal budget before migration.
// A migration that does not fit fails atomically rather than growing first and
// discovering the configured ceiling only after the schema has changed.
func OpenWithBudget(path string, budget BudgetConfig) (*Store, error) {
	return openStore(path, &budget)
}

func openStore(path string, budget *BudgetConfig) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is empty")
	}
	if path != ":memory:" {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, errors.New("database path must be a clean absolute path")
		}
		if strings.ContainsAny(path, "?#") {
			return nil, errors.New("database path must not contain SQLite URI delimiters")
		}
		parent := filepath.Dir(path)
		if err := os.MkdirAll(parent, 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		info, err := os.Lstat(parent)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, errors.New("database directory must be a real directory")
		}
		if info, err := os.Lstat(path); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return nil, errors.New("database must be a regular file, not a symlink")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := secureDatabaseFiles(path); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	statements := []string{
		"PRAGMA foreign_keys=ON", "PRAGMA synchronous=NORMAL", "PRAGMA busy_timeout=5000",
		"PRAGMA trusted_schema=OFF", "PRAGMA secure_delete=FAST", "PRAGMA journal_size_limit=33554432",
	}
	if budget == nil {
		statements = append(statements, "PRAGMA journal_mode=WAL")
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure SQLite: %w", err)
		}
	}
	if budget != nil {
		if err := store.ConfigureBudget(ctx, *budget); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure migration budget: %w", err)
		}
		if path != ":memory:" && store.budget.status.AvailableBytes < uint64(budget.MinFreeBytes+writeHeadroom(budget.MaxBytes, writeCritical)) {
			_ = db.Close()
			return nil, fmt.Errorf("%w: migration free-space watermark", ErrStorageBudget)
		}
	}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := secureDatabaseFiles(path); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func secureDatabaseFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect SQLite file: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("SQLite file %q must be regular and not a symlink", filepath.Base(candidate))
		}
		if err := os.Chmod(candidate, 0o600); err != nil {
			return fmt.Errorf("secure SQLite file permissions: %w", err)
		}
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY, incident_id TEXT NOT NULL DEFAULT '', observed_at INTEGER NOT NULL,
			kind TEXT NOT NULL, phase TEXT NOT NULL DEFAULT '', severity TEXT NOT NULL, summary TEXT NOT NULL,
			source_ip TEXT NOT NULL DEFAULT '', source_range TEXT NOT NULL DEFAULT '', target TEXT NOT NULL DEFAULT '', count INTEGER NOT NULL DEFAULT 0,
			geo_json TEXT NOT NULL DEFAULT '{}', evidence_json TEXT NOT NULL DEFAULT '{}')`,
		`CREATE INDEX IF NOT EXISTS events_observed_at_idx ON events(observed_at)`,
		`CREATE INDEX IF NOT EXISTS events_kind_idx ON events(kind, observed_at)`,
		`CREATE TABLE IF NOT EXISTS traffic_hourly (
			hour_utc INTEGER NOT NULL, direction TEXT NOT NULL, country TEXT NOT NULL, region TEXT NOT NULL,
			asn INTEGER NOT NULL, asn_org TEXT NOT NULL, attributed INTEGER NOT NULL,
			bytes INTEGER NOT NULL, packets INTEGER NOT NULL,
			PRIMARY KEY(hour_utc, direction, country, region, asn, asn_org, attributed))`,
		`CREATE TABLE IF NOT EXISTS auth_hourly (
			hour_utc INTEGER NOT NULL, kind TEXT NOT NULL, source_range TEXT NOT NULL,
			count INTEGER NOT NULL, PRIMARY KEY(hour_utc, kind, source_range))`,
		`CREATE TABLE IF NOT EXISTS auth_cardinality_hourly (
			hour_utc INTEGER PRIMARY KEY, keys INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS traffic_cardinality_hourly (
			hour_utc INTEGER PRIMARY KEY, keys INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS interface_hourly (
			hour_utc INTEGER PRIMARY KEY, rx_bytes INTEGER NOT NULL, tx_bytes INTEGER NOT NULL,
			rx_packets INTEGER NOT NULL, tx_packets INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS collector_health_hourly (
			hour_utc INTEGER PRIMARY KEY, batches INTEGER NOT NULL, overflow_bytes INTEGER NOT NULL,
			overflow_packets INTEGER NOT NULL, parse_errors INTEGER NOT NULL, kernel_packets INTEGER NOT NULL,
			kernel_drops INTEGER NOT NULL, kernel_stats_errors INTEGER NOT NULL, last_seen INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS notification_outbox (
			id TEXT PRIMARY KEY, dedupe_key TEXT NOT NULL UNIQUE, destination TEXT NOT NULL, body TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0, next_attempt INTEGER NOT NULL, last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL, sent_at INTEGER)`,
		`CREATE INDEX IF NOT EXISTS notification_pending_idx ON notification_outbox(sent_at, next_attempt)`,
		`CREATE TABLE IF NOT EXISTS report_runs (
			report_date TEXT NOT NULL, destination TEXT NOT NULL, generated_at INTEGER NOT NULL,
			PRIMARY KEY(report_date, destination))`,
		`CREATE TABLE IF NOT EXISTS component_status (
			name TEXT PRIMARY KEY, state TEXT NOT NULL, updated_at INTEGER NOT NULL)`,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, unixepoch())`,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration: %w", err)
		}
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return fmt.Errorf("read database schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("unsupported database schema version %d", version)
	}
	if version < 2 {
		for _, statement := range []string{
			`ALTER TABLE collector_health_hourly ADD COLUMN ipc_dropped_batches INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE collector_health_hourly ADD COLUMN ipc_dropped_packets INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE collector_health_hourly ADD COLUMN ipc_dropped_bytes INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE collector_health_hourly ADD COLUMN health_counter_saturations INTEGER NOT NULL DEFAULT 0`,
			`INSERT INTO schema_migrations(version, applied_at) VALUES (2, unixepoch())`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply database migration 2: %w", err)
			}
		}
	}
	if version < 3 {
		if err := migrateV3(ctx, tx); err != nil {
			return err
		}
	}
	if version < 4 {
		if err := migrateV4(ctx, tx); err != nil {
			return err
		}
	}
	if version < 5 {
		if err := migrateV5(ctx, tx); err != nil {
			return err
		}
	}
	if version < 6 {
		if err := migrateV6(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) InsertEvent(ctx context.Context, event model.Event) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()
	return insertEvent(ctx, s.db, event)
}
func insertEvent(ctx context.Context, exec executor, event model.Event) error {
	geo, err := json.Marshal(event.Geo)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(event.Evidence)
	if err != nil {
		return err
	}
	if len(geo) > 16*1024 || len(evidence) > 64*1024 {
		return errors.New("event metadata exceeds storage limit")
	}
	_, err = exec.ExecContext(ctx, `INSERT OR IGNORE INTO events
		(id, incident_id, observed_at, kind, phase, severity, summary, source_ip, source_range, target, count, geo_json, evidence_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.IncidentID, event.ObservedAt.UnixMilli(), event.Kind, event.Phase, event.Severity, limit(event.Summary, 1024),
		limit(event.SourceIP, 128), limit(event.SourceRange, 128), limit(event.Target, 256), event.Count, string(geo), string(evidence))
	return err
}

func (s *Store) AddTraffic(ctx context.Context, traffic model.Traffic) error {
	return s.AddTrafficBatch(ctx, []model.Traffic{traffic})
}

func (s *Store) AddTrafficBatch(ctx context.Context, traffic []model.Traffic) error {
	release, admitErr := s.beginWrite(ctx, writeTraffic)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	if len(traffic) == 0 {
		return nil
	}
	type trafficKey struct {
		hour            int64
		direction       model.Direction
		country, region string
		asn             uint
		asnOrg          string
		attributed      bool
	}
	type totals struct{ bytes, packets uint64 }
	aggregated := make(map[trafficKey]totals)
	for _, item := range traffic {
		if item.HourUTC.IsZero() {
			return errors.New("traffic timestamp is missing")
		}
		if item.Direction != model.DirectionInbound && item.Direction != model.DirectionOutbound {
			return errors.New("traffic direction is invalid")
		}
		if item.Bytes == 0 || item.Packets == 0 || item.Bytes > uint64(1<<63-1) || item.Packets > uint64(1<<63-1) || uint64(item.ASN) > uint64(^uint32(0)) {
			return errors.New("traffic counters or ASN are invalid")
		}
		key := trafficKey{hour: item.HourUTC.UTC().Truncate(time.Hour).Unix(), direction: item.Direction, country: limit(item.Country, 160), region: limit(item.Region, 160), asn: item.ASN, asnOrg: limit(item.ASNOrg, 160), attributed: item.Attributed}
		value := aggregated[key]
		if uint64(1<<63-1)-value.bytes < item.Bytes || uint64(1<<63-1)-value.packets < item.Packets {
			return errors.New("traffic aggregation exceeds SQLite integer bounds")
		}
		value.bytes += item.Bytes
		value.packets += item.Packets
		aggregated[key] = value
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insertStatement, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO traffic_hourly
		(hour_utc, direction, country, region, asn, asn_org, attributed, bytes, packets)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertStatement.Close()
	updateStatement, err := tx.PrepareContext(ctx, `UPDATE traffic_hourly SET bytes=bytes+?, packets=packets+?
		WHERE hour_utc=? AND direction=? AND country=? AND region=? AND asn=? AND asn_org=? AND attributed=?`)
	if err != nil {
		return err
	}
	defer updateStatement.Close()
	deleteStatement, err := tx.PrepareContext(ctx, `DELETE FROM traffic_hourly
		WHERE hour_utc=? AND direction=? AND country=? AND region=? AND asn=? AND asn_org=? AND attributed=?`)
	if err != nil {
		return err
	}
	defer deleteStatement.Close()
	keysByHour := make(map[int64]int)
	compactedByHour := make(map[int64]int64)
	loadedHours := make(map[int64]bool)
	overflow := make(map[struct {
		hour      int64
		direction model.Direction
	}]totals)
	for key, value := range aggregated {
		if !loadedHours[key.hour] {
			var keys int
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT keys FROM traffic_cardinality_hourly WHERE hour_utc=?),0)`, key.hour).Scan(&keys); err != nil {
				return err
			}
			keysByHour[key.hour] = keys
			loadedHours[key.hour] = true
		}
		arguments := []any{key.hour, key.direction, key.country, key.region, key.asn, key.asnOrg, boolInt(key.attributed)}
		result, err := insertStatement.ExecContext(ctx, append(arguments, value.bytes, value.packets)...)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 0 {
			updateArguments := []any{value.bytes, value.packets}
			updateArguments = append(updateArguments, arguments...)
			if _, err := updateStatement.ExecContext(ctx, updateArguments...); err != nil {
				return err
			}
			continue
		}
		if keysByHour[key.hour] < maxTrafficKeysPerHour {
			keysByHour[key.hour]++
			continue
		}
		if _, err := deleteStatement.ExecContext(ctx, arguments...); err != nil {
			return err
		}
		compactedByHour[key.hour]++
		overflowKey := struct {
			hour      int64
			direction model.Direction
		}{key.hour, key.direction}
		current := overflow[overflowKey]
		if uint64(1<<63-1)-current.bytes < value.bytes || uint64(1<<63-1)-current.packets < value.packets {
			return errors.New("traffic overflow aggregation exceeds SQLite integer bounds")
		}
		current.bytes += value.bytes
		current.packets += value.packets
		overflow[overflowKey] = current
	}
	for key, value := range overflow {
		if _, err := tx.ExecContext(ctx, `INSERT INTO traffic_hourly
			(hour_utc, direction, country, region, asn, asn_org, attributed, bytes, packets)
			VALUES (?, ?, '_overflow', '', 0, '', 0, ?, ?)
			ON CONFLICT(hour_utc, direction, country, region, asn, asn_org, attributed)
			DO UPDATE SET bytes=bytes+excluded.bytes, packets=packets+excluded.packets`, key.hour, key.direction, value.bytes, value.packets); err != nil {
			return err
		}
	}
	for hour, count := range compactedByHour {
		if err := recordRetention(ctx, tx, "traffic_hourly", "cardinality_compaction", count, hour*1000, (hour+3600)*1000, time.Now().UTC()); err != nil {
			return err
		}
	}
	for hour, keys := range keysByHour {
		if _, err := tx.ExecContext(ctx, `INSERT INTO traffic_cardinality_hourly(hour_utc, keys) VALUES (?, ?)
			ON CONFLICT(hour_utc) DO UPDATE SET keys=excluded.keys`, hour, keys); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AddAuth(ctx context.Context, observedAt time.Time, kind, sourceRange string) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := addAuth(ctx, tx, observedAt, kind, sourceRange); err != nil {
		return err
	}
	return tx.Commit()
}

func addAuth(ctx context.Context, tx *sql.Tx, observedAt time.Time, kind, sourceRange string) error {
	if observedAt.IsZero() || kind == "" {
		return errors.New("authentication observation is incomplete")
	}
	hour := observedAt.UTC().Truncate(time.Hour).Unix()
	kind = limit(kind, 64)
	sourceRange = limit(sourceRange, 128)
	var keys int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT keys FROM auth_cardinality_hourly WHERE hour_utc=?),0)`, hour).Scan(&keys); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO auth_hourly(hour_utc, kind, source_range, count) VALUES (?, ?, ?, 1)`, hour, kind, sourceRange)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_hourly SET count=count+1 WHERE hour_utc=? AND kind=? AND source_range=?`, hour, kind, sourceRange); err != nil {
			return err
		}
	} else if keys < maxAuthKeysPerHour {
		keys++
		if _, err := tx.ExecContext(ctx, `INSERT INTO auth_cardinality_hourly(hour_utc, keys) VALUES (?, ?)
			ON CONFLICT(hour_utc) DO UPDATE SET keys=excluded.keys`, hour, keys); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_hourly WHERE hour_utc=? AND kind=? AND source_range=?`, hour, kind, sourceRange); err != nil {
			return err
		}
		if err := recordRetention(ctx, tx, "auth_hourly", "cardinality_compaction", 1, hour*1000, (hour+3600)*1000, time.Now().UTC()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO auth_hourly(hour_utc, kind, source_range, count) VALUES (?, ?, '_overflow', 1)
			ON CONFLICT(hour_utc, kind, source_range) DO UPDATE SET count=count+1`, hour, kind); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) AddInterface(ctx context.Context, totals model.InterfaceTotals) error {
	if totals.HourUTC.IsZero() || totals.Interface != "" && !protocol.ValidInterfaceName(totals.Interface) {
		return errors.New("invalid interface totals")
	}
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	hour := totals.HourUTC.UTC().Truncate(time.Hour).Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO interface_hourly(hour_utc, rx_bytes, tx_bytes, rx_packets, tx_packets)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(hour_utc) DO UPDATE SET
		rx_bytes=rx_bytes+excluded.rx_bytes, tx_bytes=tx_bytes+excluded.tx_bytes,
		rx_packets=rx_packets+excluded.rx_packets, tx_packets=tx_packets+excluded.tx_packets`,
		hour, totals.RXBytes, totals.TXBytes, totals.RXPackets, totals.TXPackets)
	if err != nil {
		return err
	}
	if totals.Interface != "" {
		name := totals.Interface
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM interface_detail_hourly WHERE hour_utc=? AND interface=?)`, hour, name).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM interface_detail_hourly WHERE hour_utc=? AND interface<>'_overflow/'`, hour).Scan(&count); err != nil {
				return err
			}
			if count >= 32 {
				// Slash is forbidden in real interface names, avoiding collisions.
				name = "_overflow/"
				if err := recordRetention(ctx, tx, "interface_detail_hourly", "cardinality_compaction", 1, hour*1000, (hour+3600)*1000, time.Now().UTC()); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_gaps(name,reason,started_at,ended_at,count)
 SELECT 'interface_counter','interface_detail_cardinality',?,?,1
 WHERE NOT EXISTS (SELECT 1 FROM coverage_gaps WHERE name='interface_counter' AND reason='interface_detail_cardinality' AND started_at=? AND ended_at=?)`, hour*1000, (hour+3600)*1000, hour*1000, (hour+3600)*1000); err != nil {
					return err
				}
				if _, err := pruneRows(ctx, tx, "coverage_gaps", "capacity_eviction", `SELECT rowid FROM coverage_gaps WHERE id<=(SELECT MAX(id)-1000 FROM coverage_gaps)`, nil, time.Now().UTC()); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO interface_detail_hourly(hour_utc,interface,rx_bytes,tx_bytes,rx_packets,tx_packets) VALUES (?,?,?,?,?,?)
 ON CONFLICT(hour_utc,interface) DO UPDATE SET rx_bytes=rx_bytes+excluded.rx_bytes,tx_bytes=tx_bytes+excluded.tx_bytes,rx_packets=rx_packets+excluded.rx_packets,tx_packets=tx_packets+excluded.tx_packets`, hour, name, totals.RXBytes, totals.TXBytes, totals.RXPackets, totals.TXPackets); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RecordBatchHealth(ctx context.Context, batch protocol.Batch) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	hour := batch.SentAt.UTC().Truncate(time.Hour).Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO collector_health_hourly
		(hour_utc, batches, overflow_bytes, overflow_packets, parse_errors, kernel_packets, kernel_drops, kernel_stats_errors, last_seen,
		ipc_dropped_batches, ipc_dropped_packets, ipc_dropped_bytes, health_counter_saturations) VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(hour_utc) DO UPDATE SET batches=batches+1,
		overflow_bytes=overflow_bytes+excluded.overflow_bytes, overflow_packets=overflow_packets+excluded.overflow_packets,
		parse_errors=parse_errors+excluded.parse_errors, kernel_packets=kernel_packets+excluded.kernel_packets,
		kernel_drops=kernel_drops+excluded.kernel_drops, kernel_stats_errors=kernel_stats_errors+excluded.kernel_stats_errors,
		ipc_dropped_batches=ipc_dropped_batches+excluded.ipc_dropped_batches,
		ipc_dropped_packets=ipc_dropped_packets+excluded.ipc_dropped_packets,
		ipc_dropped_bytes=ipc_dropped_bytes+excluded.ipc_dropped_bytes,
		health_counter_saturations=health_counter_saturations+excluded.health_counter_saturations,
		last_seen=excluded.last_seen`,
		hour, batch.OverflowBytes, batch.OverflowPackets, batch.ParseErrors, batch.KernelPackets, batch.KernelDrops, batch.KernelStatsErrors, batch.SentAt.UnixMilli(),
		batch.IPCDroppedBatches, batch.IPCDroppedPackets, batch.IPCDroppedBytes, boolInt(batch.HealthCountersSaturated))
	return err
}

type OutboxMessage struct {
	ID          string
	DedupeKey   string
	Destination string
	Body        string
	Attempts    int
	NextAttempt time.Time
}

var ErrOutboxFull = errors.New("notification outbox capacity reached")

func (s *Store) Enqueue(ctx context.Context, message OutboxMessage) (bool, error) {
	if err := s.ExpireNotifications(ctx, time.Now().UTC()); err != nil {
		return false, err
	}
	release, admitErr := s.beginWrite(ctx, writeNormal)
	if admitErr != nil {
		return false, admitErr
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	inserted, err := enqueue(ctx, tx, message)
	if err != nil && !errors.Is(err, ErrOutboxFull) {
		return false, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return false, commitErr
	}
	return inserted, err
}

func enqueue(ctx context.Context, tx *sql.Tx, message OutboxMessage) (bool, error) {
	if message.ID == "" {
		message.ID = model.NewID("msg")
	}
	if message.DedupeKey == "" || message.Destination == "" || message.Body == "" {
		return false, errors.New("outbox message is incomplete")
	}
	if len(message.Body) > 4096 {
		return false, errors.New("outbox message exceeds Telegram limit")
	}
	if message.NextAttempt.IsZero() {
		message.NextAttempt = time.Now().UTC()
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_outbox WHERE dedupe_key=?)`, limit(message.DedupeKey, 512)).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 1 {
		return false, nil
	}
	var pendingCount, pendingBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(LENGTH(CAST(body AS BLOB))),0) FROM notification_outbox WHERE sent_at IS NULL AND suppressed_at IS NULL`).Scan(&pendingCount, &pendingBytes); err != nil {
		return false, err
	}
	if pendingCount >= maxPendingOutboxMessages || pendingBytes+int64(len(message.Body)) > maxPendingOutboxBytes {
		if _, err := tx.ExecContext(ctx, `UPDATE notification_counters SET rejected=rejected+1 WHERE id=1`); err != nil {
			return false, err
		}
		return false, ErrOutboxFull
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO notification_outbox
		(id, dedupe_key, destination, body, next_attempt, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		message.ID, limit(message.DedupeKey, 512), limit(message.Destination, 128), message.Body, message.NextAttempt.UnixMilli(), time.Now().UTC().UnixMilli(), time.Now().UTC().Add(OutboxTTL).UnixMilli())
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

func (s *Store) Pending(ctx context.Context, now time.Time, count int) ([]OutboxMessage, error) {
	if err := s.ExpireNotifications(ctx, now); err != nil {
		return nil, err
	}
	if count < 1 || count > 100 {
		count = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, dedupe_key, destination, body, attempts, next_attempt
		FROM notification_outbox AS o WHERE sent_at IS NULL AND quarantined_at IS NULL AND suppressed_at IS NULL AND expires_at > ? AND next_attempt <= ?
 AND (lease_until IS NULL OR lease_until<=?)
 AND NOT EXISTS (SELECT 1 FROM notification_cooldowns c WHERE c.destination=o.destination AND c.until_at>?)
 ORDER BY next_attempt, created_at, id LIMIT ?`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []OutboxMessage
	for rows.Next() {
		var message OutboxMessage
		var next int64
		if err := rows.Scan(&message.ID, &message.DedupeKey, &message.Destination, &message.Body, &message.Attempts, &next); err != nil {
			return nil, err
		}
		message.NextAttempt = time.UnixMilli(next).UTC()
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) MarkSent(ctx context.Context, id string, now time.Time) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET sent_at=?, last_error='',lease_until=NULL WHERE id=? AND sent_at IS NULL`, now.UnixMilli(), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE notification_counters SET last_sent_at=MAX(last_sent_at,?) WHERE id=1`, now.UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MarkFailed(ctx context.Context, id string, next time.Time, message string) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	_, err := s.db.ExecContext(ctx, `UPDATE notification_outbox SET lease_until=NULL, attempts=attempts+1, next_attempt=?, last_error=?,
 quarantined_at=CASE WHEN attempts+1>=? THEN ? ELSE quarantined_at END WHERE id=? AND sent_at IS NULL`, next.UnixMilli(), limit(message, 512), MaxDeliveryAttempts, time.Now().UTC().UnixMilli(), id)
	return err
}

func (s *Store) ReportGenerated(ctx context.Context, date, destination string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM report_runs WHERE report_date=? AND destination=?)`, date, destination).Scan(&exists)
	return exists == 1, err
}

func (s *Store) MarkReportGenerated(ctx context.Context, date, destination string, now time.Time) error {
	release, admitErr := s.beginWrite(ctx, writeNormal)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO report_runs(report_date, destination, generated_at) VALUES (?, ?, ?)`, date, destination, now.UnixMilli())
	return err
}

type ComponentStatus struct {
	Name      string    `json:"name"`
	State     string    `json:"state"`
	UpdatedAt time.Time `json:"updated_at_utc"`
}

func (s *Store) ResetComponentStatus(ctx context.Context) error {
	return s.resetCoverage(ctx, time.Now().UTC())
}
func (s *Store) SetComponentStatus(ctx context.Context, name, state string, now time.Time) error {
	return s.setCoverage(ctx, name, state, now)
}

func (s *Store) ComponentStatuses(ctx context.Context) ([]ComponentStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, state, updated_at FROM component_status ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var statuses []ComponentStatus
	for rows.Next() {
		var status ComponentStatus
		var updated int64
		if err := rows.Scan(&status.Name, &status.State, &updated); err != nil {
			return nil, err
		}
		status.UpdatedAt = time.UnixMilli(updated).UTC()
		statuses = append(statuses, status)
	}
	return statuses, rows.Err()
}

type EventCount struct {
	Kind     string
	Severity model.Severity
	Count    uint64
}
type SourceCount struct {
	SourceRange string
	Count       uint64
}
type AuthCount struct {
	Kind  string
	Count uint64
}
type TrafficCount struct {
	Direction       model.Direction
	Country, Region string
	ASN             uint
	ASNOrg          string
	Attributed      bool
	Bytes, Packets  uint64
}
type Summary struct {
	Events                                                []EventCount
	Auth                                                  []AuthCount
	TopSources                                            []SourceCount
	Traffic                                               []TrafficCount
	TrafficTruncated                                      bool
	AttributedRXBytes, AttributedTXBytes                  uint64
	Components                                            []ComponentStatus
	Coverage                                              []CoverageTotal
	Gaps                                                  []CoverageGap
	Interface                                             model.InterfaceTotals
	Batches, OverflowBytes, OverflowPackets, ParseErrors  uint64
	KernelPackets, KernelDrops, KernelStatsErrors         uint64
	IPCDroppedBatches, IPCDroppedPackets, IPCDroppedBytes uint64
	HealthCounterSaturations                              uint64
}

func (s *Store) Summary(ctx context.Context, start, end time.Time, topN int) (Summary, error) {
	if topN < 1 || topN > 50 {
		topN = 10
	}
	startMS, endMS := start.UTC().UnixMilli(), end.UTC().UnixMilli()
	startHour, endHour := start.UTC().Truncate(time.Hour).Unix(), end.UTC().Truncate(time.Hour).Unix()
	if end.UTC().After(end.UTC().Truncate(time.Hour)) {
		endHour += 3600
	}
	var summary Summary
	rows, err := s.db.QueryContext(ctx, `SELECT kind, severity, COUNT(*) FROM events WHERE observed_at>=? AND observed_at<? GROUP BY kind, severity ORDER BY COUNT(*) DESC`, startMS, endMS)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var item EventCount
		if err := rows.Scan(&item.Kind, &item.Severity, &item.Count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.Events = append(summary.Events, item)
	}
	if err := rows.Close(); err != nil {
		return summary, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT kind, SUM(count) FROM auth_hourly WHERE hour_utc>=? AND hour_utc<? GROUP BY kind ORDER BY SUM(count) DESC`, startHour, endHour)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var item AuthCount
		if err := rows.Scan(&item.Kind, &item.Count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.Auth = append(summary.Auth, item)
	}
	if err := rows.Close(); err != nil {
		return summary, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT source_range, SUM(value) FROM (
		SELECT source_range, COUNT(*) AS value FROM events
			WHERE observed_at>=? AND observed_at<? AND source_range<>''
			AND kind NOT IN ('ssh_brute_force', 'ssh_login_success') GROUP BY source_range
		UNION ALL
		SELECT source_range, SUM(count) AS value FROM auth_hourly
			WHERE hour_utc>=? AND hour_utc<? AND source_range<>'' GROUP BY source_range
	) GROUP BY source_range ORDER BY SUM(value) DESC LIMIT ?`, startMS, endMS, startHour, endHour, topN)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var item SourceCount
		if err := rows.Scan(&item.SourceRange, &item.Count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.TopSources = append(summary.TopSources, item)
	}
	if err := rows.Close(); err != nil {
		return summary, err
	}
	// Backfill may encounter many historic attribution keys. Bound display
	// rows independently of the complete attributed totals used for quality.
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN direction='inbound' THEN bytes ELSE 0 END),0),COALESCE(SUM(CASE WHEN direction='outbound' THEN bytes ELSE 0 END),0) FROM traffic_hourly WHERE hour_utc>=? AND hour_utc<? AND attributed=1`, startHour, endHour).Scan(&summary.AttributedRXBytes, &summary.AttributedTXBytes); err != nil {
		return summary, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT direction, country, region, asn, asn_org, attributed, SUM(bytes), SUM(packets)
		FROM traffic_hourly WHERE hour_utc>=? AND hour_utc<? GROUP BY direction, country, region, asn, asn_org, attributed ORDER BY SUM(bytes) DESC,direction,country,region,asn,asn_org,attributed LIMIT 4097`, startHour, endHour)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var item TrafficCount
		if len(summary.Traffic) >= 4096 {
			summary.TrafficTruncated = true
			break
		}
		var attributed int
		if err := rows.Scan(&item.Direction, &item.Country, &item.Region, &item.ASN, &item.ASNOrg, &attributed, &item.Bytes, &item.Packets); err != nil {
			rows.Close()
			return summary, err
		}
		item.Attributed = attributed == 1
		summary.Traffic = append(summary.Traffic, item)
	}
	if err := rows.Close(); err != nil {
		return summary, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(rx_bytes),0), COALESCE(SUM(tx_bytes),0), COALESCE(SUM(rx_packets),0), COALESCE(SUM(tx_packets),0) FROM interface_hourly WHERE hour_utc>=? AND hour_utc<?`, startHour, endHour).Scan(&summary.Interface.RXBytes, &summary.Interface.TXBytes, &summary.Interface.RXPackets, &summary.Interface.TXPackets); err != nil {
		return summary, fmt.Errorf("read interface summary: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(batches),0), COALESCE(SUM(overflow_bytes),0), COALESCE(SUM(overflow_packets),0), COALESCE(SUM(parse_errors),0), COALESCE(SUM(kernel_packets),0), COALESCE(SUM(kernel_drops),0), COALESCE(SUM(kernel_stats_errors),0),
		COALESCE(SUM(ipc_dropped_batches),0), COALESCE(SUM(ipc_dropped_packets),0), COALESCE(SUM(ipc_dropped_bytes),0), COALESCE(SUM(health_counter_saturations),0)
		FROM collector_health_hourly WHERE hour_utc>=? AND hour_utc<?`, startHour, endHour).Scan(&summary.Batches, &summary.OverflowBytes, &summary.OverflowPackets, &summary.ParseErrors, &summary.KernelPackets, &summary.KernelDrops, &summary.KernelStatsErrors,
		&summary.IPCDroppedBatches, &summary.IPCDroppedPackets, &summary.IPCDroppedBytes, &summary.HealthCounterSaturations); err != nil {
		return summary, fmt.Errorf("read sensor health summary: %w", err)
	}
	summary.Gaps, err = s.CoverageGaps(ctx, start, end, 4)
	if err != nil {
		return summary, err
	}
	summary.Coverage, err = s.Coverage(ctx, start, end)
	if err != nil {
		return summary, err
	}
	summary.Components, err = s.ComponentStatuses(ctx)
	if err != nil {
		return summary, err
	}
	return summary, nil
}

func (s *Store) Prune(ctx context.Context, now time.Time) error {
	if err := s.ExpireNotifications(ctx, now); err != nil {
		return err
	}
	eventCutoff := now.AddDate(0, 0, -7).UnixMilli()
	hourCutoff := now.AddDate(-1, -1, 0).UTC().Truncate(time.Hour).Unix()
	reportCutoff := now.AddDate(-1, -1, 0).UnixMilli()
	outboxCutoff := now.AddDate(0, 0, -30).UnixMilli()
	for _, query := range []struct {
		table, condition string
		arg              int64
	}{
		{"events", "observed_at<?", eventCutoff}, {"traffic_hourly", "hour_utc<?", hourCutoff}, {"auth_hourly", "hour_utc<?", hourCutoff},
		{"auth_cardinality_hourly", "hour_utc<?", hourCutoff}, {"traffic_cardinality_hourly", "hour_utc<?", hourCutoff}, {"interface_hourly", "hour_utc<?", hourCutoff},
		{"interface_detail_hourly", "hour_utc<?", hourCutoff},
		{"collector_health_hourly", "hour_utc<?", hourCutoff}, {"notification_outbox", "sent_at IS NOT NULL AND sent_at<?", outboxCutoff},
		{"report_runs", "generated_at<?", reportCutoff}, {"report_snapshots", "generated_at<?", reportCutoff}, {"coverage_intervals", "ended_at IS NOT NULL AND ended_at<?", reportCutoff},
	} {
		// Bound dirty pages and work per pass. Large legacy backlogs drain across
		// subsequent maintenance passes without one database-sized transaction.
		for pass := 0; pass < 64; pass++ {
			release, err := s.beginWrite(ctx, writeCritical)
			if err != nil {
				return err
			}
			count, err := s.pruneRetentionChunkLocked(ctx, query.table, "time_expiry", "SELECT rowid FROM "+query.table+" WHERE "+query.condition+" LIMIT 256", []any{query.arg}, now)
			release()
			if err != nil {
				return err
			}
			if count < 256 {
				break
			}
		}
	}
	return nil
}

func limit(value string, max int) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	if len(value) > max {
		value = value[:max]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

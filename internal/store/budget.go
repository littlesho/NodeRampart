// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// The active-file budget reserves one original rollback-journal image (page+8
// bytes) per database page, plus header/sidecar overhead. Disabling cache spills
// prevents extra journal-header segments before commit. This mode trades WAL
// concurrency and dirty-page memory for a bound on database+journal file sizes.
// Backup outputs, query/statement temporaries, and filesystem metadata are not
// part of this active-file bound. See docs/STORAGE-BUDGET.md.
const budgetFileOverhead int64 = 1 << 20

var ErrStorageBudget = errors.New("storage budget prevents write")

type BudgetConfig struct {
	MaxBytes     int64 `json:"max_bytes"`
	MinFreeBytes int64 `json:"min_free_bytes"`
}

type StorageBudgetStatus struct {
	Configured         bool      `json:"configured"`
	State              string    `json:"state"`
	Reason             string    `json:"reason,omitempty"`
	JournalMode        string    `json:"journal_mode"`
	MaxBytes           int64     `json:"max_bytes"`
	DatabaseLimitBytes int64     `json:"database_limit_bytes"`
	DatabaseBytes      int64     `json:"database_bytes"`
	WALBytes           int64     `json:"wal_bytes"`
	JournalBytes       int64     `json:"journal_bytes"`
	SHMBytes           int64     `json:"shm_bytes"`
	UsedBytes          int64     `json:"used_bytes"`
	LiveBytes          int64     `json:"live_bytes"`
	AvailableBytes     uint64    `json:"available_bytes"`
	MinFreeBytes       int64     `json:"min_free_bytes"`
	RejectedWrites     uint64    `json:"rejected_writes"`
	PrunedTrafficRows  uint64    `json:"pruned_traffic_rows"`
	CompactedAuthRows  uint64    `json:"compacted_auth_rows"`
	PrunedEventRows    uint64    `json:"pruned_event_rows"`
	LastCheckedUTC     time.Time `json:"last_checked_utc"`
}

type budgetState struct {
	config             BudgetConfig
	pageSize, maxPages int64
	status             StorageBudgetStatus
	freeSpace          func(string) (uint64, error)
	authCursor         *authCompactionCursor
	authPassCompleted  bool
}

type writePriority int

const (
	writeCritical writePriority = iota
	writeNormal
	writeTraffic
)

func (s *Store) ConfigureBudget(ctx context.Context, cfg BudgetConfig) error {
	if cfg.MaxBytes < 64<<20 || cfg.MaxBytes > 1<<40 || cfg.MinFreeBytes < 0 || cfg.MinFreeBytes > 1<<40 {
		return errors.New("storage budget requires max_bytes 64 MiB..1 TiB and min_free_bytes 0..1 TiB")
	}
	if err := s.lockBudget(ctx); err != nil {
		return err
	}
	defer s.budgetMu.Unlock()
	var pageSize int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return err
	}
	if pageSize < 512 || pageSize > 65536 {
		return errors.New("unsupported SQLite page size")
	}
	state := &budgetState{config: cfg, pageSize: pageSize, maxPages: (cfg.MaxBytes - budgetFileOverhead) / (2*pageSize + 8), freeSpace: availableDiskBytes}
	state.status = StorageBudgetStatus{Configured: true, State: "running", MaxBytes: cfg.MaxBytes, MinFreeBytes: cfg.MinFreeBytes, DatabaseLimitBytes: state.maxPages * pageSize}
	s.budget = state
	if err := s.configureBudgetConnection(ctx); err != nil {
		state.status.State, state.status.Reason = "degraded", "budget_configuration_failed"
		return err
	}
	_, err := s.budgetStatusLocked(ctx)
	return err
}

// configureBudgetConnection also runs before writes: max_page_count and cache
// settings belong to a connection and must survive a driver reconnect. Holding
// SQLite's exclusive lock prevents another connection from changing modes or
// growing the file without this limit while the daemon owns it.
func (s *Store) configureBudgetConnection(ctx context.Context) error {
	state := s.budget
	var mode string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return err
	}
	if mode == "wal" {
		var busy, log, checkpointed int
		if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil {
			return err
		}
		if busy != 0 {
			return errors.New("storage budget requires an unblocked WAL checkpoint")
		}
	}
	if s.path != ":memory:" {
		if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
			return err
		}
		if mode != "delete" {
			return errors.New("storage budget requires DELETE journal mode")
		}
	}
	for _, statement := range []string{`PRAGMA synchronous=FULL`, `PRAGMA cache_spill=OFF`, `PRAGMA temp_store=MEMORY`, `PRAGMA journal_size_limit=0`, `PRAGMA locking_mode=EXCLUSIVE`} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	var actual int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`PRAGMA max_page_count=%d`, state.maxPages)).Scan(&actual); err != nil {
		return err
	}
	if actual != state.maxPages {
		return fmt.Errorf("existing database exceeds storage page budget; increase storage.max_bytes or restore a compact backup")
	}
	if _, err := s.db.ExecContext(ctx, `BEGIN EXCLUSIVE`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `COMMIT`); err != nil {
		_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`)
		return err
	}
	return nil
}

// beginWrite serializes the admission decision with the mutation. Callers must
// call release after the transaction has committed or rolled back, and avoid
// nesting other public write methods while holding this guard.
func (s *Store) beginWrite(ctx context.Context, priority writePriority) (func(), error) {
	if err := s.lockBudget(ctx); err != nil {
		return nil, err
	}
	if s.budget == nil {
		return s.budgetMu.Unlock, nil
	}
	fail := func(reason string, err error) (func(), error) {
		s.budget.status.State, s.budget.status.Reason = "degraded", reason
		if s.budget.status.RejectedWrites < ^uint64(0) {
			s.budget.status.RejectedWrites++
		}
		s.budgetMu.Unlock()
		return nil, fmt.Errorf("%w: %s: %w", ErrStorageBudget, reason, err)
	}
	if err := s.configureBudgetConnection(ctx); err != nil {
		return fail("budget_configuration_failed", err)
	}
	status, err := s.budgetStatusLocked(ctx)
	if err != nil {
		return fail("storage_inspection_failed", err)
	}
	if s.path != ":memory:" && status.AvailableBytes < uint64(s.budget.config.MinFreeBytes+writeHeadroom(s.budget.config.MaxBytes, priority)) {
		return fail("low_disk_space", errors.New("free-space watermark reached"))
	}
	if status.UsedBytes > status.MaxBytes {
		return fail("active_files_over_budget", errors.New("active database files exceed configured budget"))
	}
	// Critical authentication writes can grow source detail, too. Give every
	// writer one bounded reclamation step before consuming the reserve. Freed
	// pages are reusable without shrinking the physical database.
	reserve := s.budget.status.DatabaseLimitBytes / 8
	if status.LiveBytes >= status.DatabaseLimitBytes-reserve {
		if _, err := s.pruneBudgetChunkLocked(ctx, false); err != nil {
			return fail("budget_prune_failed", err)
		}
		status, err = s.budgetStatusLocked(ctx)
		if err != nil {
			return fail("storage_inspection_failed", err)
		}
		if priority != writeCritical && status.LiveBytes >= status.DatabaseLimitBytes-reserve {
			return fail("reserved_event_capacity", errors.New("database reserve is for critical evidence"))
		}
	}
	// Keep the inspected degraded status even when a critical in-place update
	// or deletion is allowed. Admission is not proof of recovered capacity.
	if status.LiveBytes < status.DatabaseLimitBytes-reserve && (s.path == ":memory:" || status.AvailableBytes >= uint64(status.MinFreeBytes+writeHeadroom(status.MaxBytes, writeNormal))) {
		s.budget.status.State, s.budget.status.Reason = "running", ""
	}
	return s.budgetMu.Unlock, nil
}

func writeHeadroom(maxBytes int64, priority writePriority) int64 {
	headroom := maxBytes / 16
	if headroom > 64<<20 {
		headroom = 64 << 20
	}
	if priority == writeCritical {
		headroom /= 4
	}
	return headroom
}

func (s *Store) BudgetStatus(ctx context.Context) (StorageBudgetStatus, error) {
	if err := ctx.Err(); err != nil {
		return StorageBudgetStatus{}, err
	}
	if !s.budgetMu.TryLock() {
		return StorageBudgetStatus{State: "busy", Reason: "storage_operation_in_progress"}, nil
	}
	defer s.budgetMu.Unlock()
	return s.budgetStatusLocked(ctx)
}

func (s *Store) lockBudget(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.budgetMu.TryLock() {
		return nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if s.budgetMu.TryLock() {
				return nil
			}
		}
	}
}

func (s *Store) budgetStatusLocked(ctx context.Context) (StorageBudgetStatus, error) {
	status := StorageBudgetStatus{State: "unconfigured"}
	if s.budget != nil {
		status = s.budget.status
	}
	var pageSize, pages, freePages int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return status, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return status, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freePages); err != nil {
		return status, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&status.JournalMode); err != nil {
		return status, err
	}
	status.LiveBytes = (pages - freePages) * pageSize
	if s.path != ":memory:" {
		for _, item := range []struct {
			path  string
			value *int64
		}{{s.path, &status.DatabaseBytes}, {s.path + "-wal", &status.WALBytes}, {s.path + "-journal", &status.JournalBytes}, {s.path + "-shm", &status.SHMBytes}} {
			info, err := os.Lstat(item.path)
			if errors.Is(err, os.ErrNotExist) {
				*item.value = 0
				continue
			}
			if err != nil {
				return status, err
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return status, errors.New("unsafe SQLite sidecar")
			}
			*item.value = info.Size()
		}
		space := availableDiskBytes
		if s.budget != nil {
			space = s.budget.freeSpace
		}
		var err error
		status.AvailableBytes, err = space(filepath.Dir(s.path))
		if err != nil {
			return status, err
		}
	}
	status.UsedBytes = status.DatabaseBytes + status.WALBytes + status.JournalBytes + status.SHMBytes
	status.LastCheckedUTC = time.Now().UTC()
	if s.budget != nil {
		if status.Reason == "active_files_over_budget" || status.Reason == "low_disk_space" || status.Reason == "reserved_event_capacity" || status.Reason == "database_capacity_exhausted" {
			status.State, status.Reason = "running", ""
		}
		if status.UsedBytes > status.MaxBytes {
			status.State, status.Reason = "degraded", "active_files_over_budget"
		} else if s.path != ":memory:" && status.AvailableBytes < uint64(status.MinFreeBytes+writeHeadroom(status.MaxBytes, writeNormal)) {
			status.State, status.Reason = "degraded", "low_disk_space"
		} else if status.LiveBytes >= status.DatabaseLimitBytes {
			status.State, status.Reason = "degraded", "database_capacity_exhausted"
		} else if status.LiveBytes >= status.DatabaseLimitBytes-status.DatabaseLimitBytes/8 {
			status.State, status.Reason = "degraded", "reserved_event_capacity"
		}
		s.budget.status = status
	}
	return status, nil
}

func availableDiskBytes(path string) (uint64, error) {
	var info unix.Statfs_t
	if err := unix.Statfs(path, &info); err != nil {
		return 0, err
	}
	if info.Bsize <= 0 {
		return 0, errors.New("invalid filesystem block size")
	}
	if info.Bavail > ^uint64(0)/uint64(info.Bsize) {
		return ^uint64(0), nil
	}
	return info.Bavail * uint64(info.Bsize), nil
}

// MaintainBudget performs at most one bounded reclamation transaction. Traffic
// is removed first, followed by authentication source compaction, then events.
// It intentionally reuses freelist pages without an unbounded runtime VACUUM.
func (s *Store) MaintainBudget(ctx context.Context, now time.Time) error {
	if err := s.lockBudget(ctx); err != nil {
		return err
	}
	defer s.budgetMu.Unlock()
	if s.budget == nil {
		return nil
	}
	if err := s.configureBudgetConnection(ctx); err != nil {
		return err
	}
	status, err := s.budgetStatusLocked(ctx)
	if err != nil {
		return err
	}
	if status.LiveBytes < status.DatabaseLimitBytes-status.DatabaseLimitBytes/8 {
		return nil
	}
	if status.AvailableBytes < uint64(s.budget.config.MinFreeBytes+writeHeadroom(s.budget.config.MaxBytes, writeCritical)) && s.path != ":memory:" {
		s.budget.status.State, s.budget.status.Reason = "degraded", "low_disk_space"
		return fmt.Errorf("%w: free-space watermark prevents maintenance", ErrStorageBudget)
	}
	_, err = s.pruneBudgetChunkLocked(ctx, true)
	if err != nil {
		s.budget.status.State, s.budget.status.Reason = "degraded", "budget_prune_failed"
		return err
	}
	_, err = s.budgetStatusLocked(ctx)
	return err
}

func (s *Store) pruneBudgetChunkLocked(ctx context.Context, allowEvents bool) (bool, error) {
	// Traffic pruning conservatively leaves its cardinality counters in place.
	rows, err := s.pruneRetentionChunkLocked(ctx, "traffic_hourly", "storage_pressure", `SELECT rowid FROM traffic_hourly ORDER BY hour_utc LIMIT 256`, nil, time.Now().UTC())
	if err != nil {
		return false, err
	}
	if rows > 0 {
		s.budget.status.PrunedTrafficRows += uint64(rows)
		return true, nil
	}
	// A critical/normal writer must not consume the completed auth pass and
	// starve event maintenance by repeatedly starting the next walk. Keep its
	// completion latched until an event-capable maintenance call uses it.
	if !allowEvents || !s.budget.authPassCompleted {
		if progressed, err := s.compactAuthChunkLocked(ctx); err != nil || progressed {
			return progressed, err
		}
	}
	if !allowEvents {
		return false, nil
	}
	rows, err = s.pruneRetentionChunkLocked(ctx, "events", "storage_pressure", `SELECT rowid FROM events ORDER BY observed_at LIMIT 64`, nil, time.Now().UTC())
	if err != nil {
		return false, err
	}
	s.budget.authPassCompleted = false
	s.budget.status.PrunedEventRows += uint64(rows)
	return rows > 0, nil
}

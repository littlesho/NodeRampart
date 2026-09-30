// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ForeignKeyViolation contains schema names only, never event bodies or IDs.
type ForeignKeyViolation struct {
	Table  string `json:"table"`
	Parent string `json:"parent"`
}

type ForeignKeyStatus struct {
	Violations []ForeignKeyViolation `json:"violations"`
	Truncated  bool                  `json:"truncated"`
}

var ErrForeignKeySnapshotUnavailable = errors.New("foreign-key snapshot could not be inspected reliably; use a safe standalone copy with a supported schema")

// InspectForeignKeysSnapshot diagnoses a standalone forensic copy without
// migration, repair or deletion. Unlike VerifyBackup, it returns retained
// violations so a legacy database that cannot migrate can still be examined.
// WAL/SHM/journal sidecars must first be preserved as part of the original
// evidence; their presence never authorizes ignoring or deleting them here.
func InspectForeignKeysSnapshot(ctx context.Context, path string) (status ForeignKeyStatus, err error) {
	status.Violations = []ForeignKeyViolation{}
	if err := ctx.Err(); err != nil {
		return status, err
	}
	parent, base, err := secureParent(path)
	if err != nil {
		return status, ErrForeignKeySnapshotUnavailable
	}
	defer parent.Close()
	file, err := openSnapshot(parent, base)
	if err != nil {
		return status, ErrForeignKeySnapshotUnavailable
	}
	defer file.Close()
	var before unix.Stat_t
	if unix.Fstat(int(file.Fd()), &before) != nil || before.Nlink != 1 || before.Mode&0o022 != 0 || before.Size <= 0 || before.Size > 1<<40 || before.Uid != 0 && before.Uid != uint32(os.Geteuid()) {
		return status, ErrForeignKeySnapshotUnavailable
	}
	db, err := openReadOnlySnapshot(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
	if err != nil {
		return status, ErrForeignKeySnapshotUnavailable
	}
	defer func() {
		if closeErr := db.Close(); err == nil && closeErr != nil {
			err = ErrForeignKeySnapshotUnavailable
		}
	}()
	if _, err := db.ExecContext(ctx, `PRAGMA query_only=ON`); err != nil {
		if ctx.Err() != nil {
			return status, ctx.Err()
		}
		return status, ErrForeignKeySnapshotUnavailable
	}
	if _, err := verifySnapshotSchema(ctx, db); err != nil {
		if ctx.Err() != nil {
			return status, ctx.Err()
		}
		return status, ErrForeignKeySnapshotUnavailable
	}
	status, err = checkForeignKeys(ctx, db)
	if err != nil {
		if ctx.Err() != nil {
			return status, ctx.Err()
		}
		return status, ErrForeignKeySnapshotUnavailable
	}
	var after unix.Stat_t
	if unix.Fstat(int(file.Fd()), &after) != nil || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return status, ErrForeignKeySnapshotUnavailable
	}
	for _, name := range []string{base + "-wal", base + "-shm", base + "-journal"} {
		var sidecar unix.Stat_t
		if err := unix.Fstatat(int(parent.Fd()), name, &sidecar, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
			return status, ErrForeignKeySnapshotUnavailable
		}
	}
	return status, nil
}

// ForeignKeys detects retained orphans read-only. Enabling foreign_keys does
// not repair historical violations; never delete unknown history here.
func (s *Store) ForeignKeys(ctx context.Context) (ForeignKeyStatus, error) {
	return checkForeignKeys(ctx, s.db)
}

func checkForeignKeys(ctx context.Context, db *sql.DB) (ForeignKeyStatus, error) {
	status := ForeignKeyStatus{Violations: []ForeignKeyViolation{}}
	rows, err := db.QueryContext(ctx, `SELECT "table",parent FROM pragma_foreign_key_check LIMIT 101`)
	if err != nil {
		return status, err
	}
	defer rows.Close()
	for rows.Next() {
		var violation ForeignKeyViolation
		if err := rows.Scan(&violation.Table, &violation.Parent); err != nil {
			return status, err
		}
		if len(status.Violations) == 100 {
			status.Truncated = true
			break
		}
		status.Violations = append(status.Violations, violation)
	}
	return status, rows.Err()
}

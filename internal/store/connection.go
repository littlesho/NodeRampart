// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"sync/atomic"

	"modernc.org/sqlite"
)

// Common connection attributes use the driver's supported DSN initialization.
// A small per-Store connector handles the budget that depends on the actual
// database page size, and ConfigureBudget on legacy offline Open callers.
// No process-global hooks or driver registrations carry database-local state.
type storeConnector struct {
	path   string
	budget atomic.Pointer[BudgetConfig]
}

func connectionParameters(budget bool) url.Values {
	q := url.Values{}
	for _, value := range []string{"busy_timeout=5000", "foreign_keys=ON", "trusted_schema=OFF", "secure_delete=FAST"} {
		q.Add("_pragma", value)
	}
	if budget {
		for _, value := range []string{"journal_mode=DELETE", "synchronous=FULL", "cache_spill=OFF", "temp_store=MEMORY", "journal_size_limit=0", "locking_mode=EXCLUSIVE"} {
			q.Add("_pragma", value)
		}
	} else {
		for _, value := range []string{"journal_mode=WAL", "synchronous=NORMAL", "journal_size_limit=33554432"} {
			q.Add("_pragma", value)
		}
	}
	return q
}

func (c *storeConnector) Driver() driver.Driver { return &sqlite.Driver{} }

func (c *storeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	budget := c.budget.Load()
	base, err := sqlite.NewConnector(c.path + "?" + connectionParameters(budget != nil).Encode())
	if err != nil {
		return nil, err
	}
	conn, err := base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (driver.Conn, error) { _ = conn.Close(); return nil, err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if budget == nil {
		return conn, nil
	}
	query := conn.(driver.QueryerContext)
	exec := conn.(driver.ExecerContext)
	pageSize, err := connectionInt(ctx, query, "PRAGMA page_size")
	if err != nil {
		return fail(err)
	}
	if pageSize < 512 || pageSize > 65536 {
		return fail(errors.New("unsupported SQLite page size"))
	}
	maxPages := (budget.MaxBytes - budgetFileOverhead) / (2*pageSize + 8)
	actual, err := connectionInt(ctx, query, fmt.Sprintf("PRAGMA max_page_count=%d", maxPages))
	if err != nil {
		return fail(err)
	}
	if actual != maxPages {
		return fail(errors.New("existing database exceeds storage page budget"))
	}
	// EXCLUSIVE is a connection policy; acquiring its lock before publication
	// prevents another writer bypassing the ceiling during this connection.
	if _, err := exec.ExecContext(ctx, "BEGIN EXCLUSIVE", nil); err != nil {
		return fail(err)
	}
	if _, err := exec.ExecContext(ctx, "COMMIT", nil); err != nil {
		return fail(err)
	}
	return conn, nil
}

func connectionInt(ctx context.Context, query driver.QueryerContext, statement string) (int64, error) {
	rows, err := query.QueryContext(ctx, statement, nil)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return 0, err
	}
	value, ok := values[0].(int64)
	if !ok {
		return 0, errors.New("invalid SQLite initialization result")
	}
	return value, nil
}

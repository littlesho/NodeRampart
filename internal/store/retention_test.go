// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func retentionStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func retentionCount(t *testing.T, s *Store, dataset, reason string) int64 {
	t.Helper()
	var count int64
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(affected_rows),0) FROM retention_totals WHERE dataset=? AND reason=?`, dataset, reason).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func failRetention(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_ledger BEFORE INSERT ON retention_ledger BEGIN SELECT RAISE(ABORT,'synthetic ledger failure'); END`); err != nil {
		t.Fatal(err)
	}
}
func seedGaps(t *testing.T, s *Store, at time.Time) {
	t.Helper()
	if _, err := s.db.Exec(`WITH RECURSIVE n(v) AS (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<1000) INSERT INTO coverage_gaps(name,reason,started_at,ended_at,count) SELECT 'ssh_journal','cursor_invalid',?,?,1 FROM n`, at.UnixMilli(), at.Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionExpiryAllDatasetsAndNoHousekeepingDoubleCount(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	old := now.AddDate(-1, -2, 0)
	e, m := alertFixture("old_evidence", "inc_old", "start")
	e.ObservedAt = old
	if err := s.InsertEventNotification(ctx, e, m); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSent(ctx, m.ID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE notification_outbox SET created_at=?`, old.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	queries := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO traffic_hourly VALUES (?,'inbound','ZZ','',0,'',0,1,1)`, []any{old.Unix()}},
		{`INSERT INTO auth_hourly VALUES (?,'failure','synthetic',1)`, []any{old.Unix()}},
		{`INSERT INTO auth_cardinality_hourly VALUES (?,1)`, []any{old.Unix()}},
		{`INSERT INTO traffic_cardinality_hourly VALUES (?,1)`, []any{old.Unix()}},
		{`INSERT INTO interface_hourly VALUES (?,1,1,1,1)`, []any{old.Unix()}},
		{`INSERT INTO interface_detail_hourly VALUES (?,'lab0',1,1,1,1)`, []any{old.Unix()}},
		{`INSERT INTO collector_health_hourly(hour_utc,batches,overflow_bytes,overflow_packets,parse_errors,kernel_packets,kernel_drops,kernel_stats_errors,last_seen) VALUES (?,1,0,0,0,1,0,0,?)`, []any{old.Unix(), old.UnixMilli()}},
		{`INSERT INTO report_runs VALUES ('2024-01-01','local',?)`, []any{old.UnixMilli()}},
		{`INSERT INTO report_snapshots(report_date,title,body,period_start,period_end,generated_at) VALUES ('2024-01-01','synthetic','synthetic',?,?,?)`, []any{old.UnixMilli(), old.Add(time.Hour).UnixMilli(), old.UnixMilli()}},
		{`INSERT INTO coverage_intervals(name,state,started_at,ended_at) VALUES ('sensor_feed','running',?,?)`, []any{old.UnixMilli(), old.Add(time.Minute).UnixMilli()}},
	}
	for _, q := range queries {
		if _, err := s.db.Exec(q.q, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Prune(ctx, now); err != nil {
		t.Fatal(err)
	}
	want := []string{"events", "traffic_hourly", "auth_hourly", "interface_hourly", "interface_detail_hourly", "collector_health_hourly", "report_snapshots", "coverage_intervals", "notification_outbox"}
	for _, dataset := range want {
		if count := retentionCount(t, s, dataset, "time_expiry"); count != 1 {
			t.Fatalf("%s count=%d", dataset, count)
		}
	}
	var decisions, totals int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM event_notifications`).Scan(&decisions); err != nil || decisions != 0 {
		t.Fatal("cascade missing", decisions, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM retention_totals`).Scan(&totals); err != nil || totals != len(want) {
		t.Fatal("housekeeping double-counted", totals, err)
	}
	if err := s.Prune(ctx, now); err != nil {
		t.Fatal(err)
	}
	if retentionCount(t, s, "events", "time_expiry") != 1 {
		t.Fatal("empty delete counted")
	}
	view, err := s.Retention(ctx, RetentionQuery{Start: old.Add(-time.Hour), End: old.Add(2 * time.Hour), Limit: 3})
	if err != nil || len(view.Entries) != 3 || !view.More || view.NextBeforeID == 0 {
		t.Fatal(view, err)
	}
	for _, r := range view.Retained {
		if r.Rows != 0 {
			t.Fatal("remaining rows fabricated", r)
		}
	}
	view, err = s.Retention(ctx, RetentionQuery{Start: now.Add(-time.Hour), End: now, Dataset: "events", Limit: 1})
	if err != nil || len(view.Entries) != 0 || len(view.Totals) != 1 {
		t.Fatal("totals incorrectly time filtered", view, err)
	}
}

func TestRetentionLedgerFailureRollsBackExpiryAndPressure(t *testing.T) {
	for _, mode := range []string{"expiry", "traffic_pressure", "event_pressure"} {
		t.Run(mode, func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			now := time.Now().UTC()
			e, m := alertFixture("rollback_event", "inc_rollback", "start")
			e.ObservedAt = now.Add(-8 * 24 * time.Hour)
			if err := s.InsertEventNotification(ctx, e, m); err != nil {
				t.Fatal(err)
			}
			if err := s.AddTraffic(ctx, model.Traffic{HourUTC: now, Direction: model.DirectionInbound, Bytes: 1, Packets: 1}); err != nil {
				t.Fatal(err)
			}
			if mode == "event_pressure" {
				if _, err := s.db.Exec(`DELETE FROM traffic_hourly`); err != nil {
					t.Fatal(err)
				}
				s.budget.authPassCompleted = true
			}
			failRetention(t, s)
			var err error
			if mode == "expiry" {
				err = s.Prune(ctx, now)
			} else {
				s.budgetMu.Lock()
				_, err = s.pruneBudgetChunkLocked(ctx, mode == "event_pressure")
				s.budgetMu.Unlock()
			}
			if err == nil {
				t.Fatal("ledger failure swallowed")
			}
			var events, decisions int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM event_notifications`).Scan(&decisions); err != nil {
				t.Fatal(err)
			}
			if events != 1 || decisions != 1 {
				t.Fatal("event/cascade survived partial commit", events, decisions)
			}
			if mode == "traffic_pressure" {
				var rows int
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM traffic_hourly`).Scan(&rows); err != nil || rows != 1 {
					t.Fatal(rows, err)
				}
			}
			if s.budget.status.PrunedTrafficRows != 0 || s.budget.status.PrunedEventRows != 0 {
				t.Fatal("in-memory count advanced on rollback")
			}
		})
	}
}

func TestRetentionCoverageCapacityAllThreePaths(t *testing.T) {
	for _, mode := range []string{"gap", "interface", "auth"} {
		t.Run(mode, func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Hour)
			seedGaps(t, s, now.Add(-time.Hour))
			switch mode {
			case "gap":
				if err := s.RecordCoverageGap(ctx, CoverageGap{Name: "sensor_feed", Reason: "restart", Start: now, End: now}); err != nil {
					t.Fatal(err)
				}
			case "interface":
				for i := 0; i < 33; i++ {
					if err := s.AddInterface(ctx, model.InterfaceTotals{Interface: fmt.Sprintf("lab%d", i), HourUTC: now, RXBytes: 1}); err != nil {
						t.Fatal(err)
					}
				}
			case "auth":
				seedAuthDetail(t, s, now, 4)
				if _, err := compactAuthForTest(t, s); err != nil {
					t.Fatal(err)
				}
			}
			if count := retentionCount(t, s, "coverage_gaps", "capacity_eviction"); count != 1 {
				t.Fatal(mode, count)
			}
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM coverage_gaps`).Scan(&count); err != nil || count != 1000 {
				t.Fatal(count, err)
			}
			if mode == "auth" && retentionCount(t, s, "auth_hourly", "storage_pressure") != 4 {
				t.Fatal("auth source rows not recorded")
			}
			if mode == "interface" && retentionCount(t, s, "interface_detail_hourly", "cardinality_compaction") != 1 {
				t.Fatal("interface folding not recorded")
			}
		})
	}
}

func TestRetentionCoverageIntervalsAndOpenRemaining(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if _, err := s.db.Exec(`WITH RECURSIVE n(v) AS (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<10000) INSERT INTO coverage_intervals(name,state,started_at,ended_at) SELECT 'ssh_journal','running',?,? FROM n`, now.Add(-time.Hour).UnixMilli(), now.Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := s.setCoverage(ctx, "sensor_feed", "running", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatCoverage(ctx, now); err != nil {
		t.Fatal(err)
	}
	if retentionCount(t, s, "coverage_intervals", "capacity_eviction") != 1 {
		t.Fatal("closed coverage not accounted")
	}
	view, err := s.Retention(ctx, RetentionQuery{Start: now.Add(-30 * time.Second), End: now.Add(time.Second), Dataset: "coverage_intervals", Limit: 10})
	if err != nil || len(view.Retained) != 1 || view.Retained[0].Rows != 1 || !view.Retained[0].End.Equal(now) {
		t.Fatal("open interval missed", view, err)
	}
}

func TestRetentionCompactionPreservesTotalsAndRollsBack(t *testing.T) {
	for _, dataset := range []string{"traffic_hourly", "auth_hourly"} {
		t.Run(dataset, func(t *testing.T) {
			s := retentionStore(t)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Hour)
			var write func() error
			if dataset == "traffic_hourly" {
				if _, err := s.db.Exec(`INSERT INTO traffic_cardinality_hourly VALUES (?,?)`, now.Unix(), maxTrafficKeysPerHour); err != nil {
					t.Fatal(err)
				}
				write = func() error {
					return s.AddTraffic(ctx, model.Traffic{HourUTC: now, Direction: model.DirectionInbound, Country: "ZZ", Bytes: 7, Packets: 3})
				}
			}
			if dataset == "auth_hourly" {
				if _, err := s.db.Exec(`INSERT INTO auth_cardinality_hourly VALUES (?,?)`, now.Unix(), maxAuthKeysPerHour); err != nil {
					t.Fatal(err)
				}
				write = func() error { return s.AddAuth(ctx, now, "failure", "synthetic") }
			}
			failRetention(t, s)
			if err := write(); err == nil {
				t.Fatal("compaction committed without ledger")
			}
			var rows int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + dataset).Scan(&rows); err != nil || rows != 0 {
				t.Fatal("partial data survived", rows, err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER fail_ledger`); err != nil {
				t.Fatal(err)
			}
			if err := write(); err != nil {
				t.Fatal(err)
			}
			if retentionCount(t, s, dataset, "cardinality_compaction") != 1 {
				t.Fatal("compaction count missing")
			}
			var count int64
			query := `SELECT SUM(count) FROM auth_hourly`
			want := int64(1)
			if dataset == "traffic_hourly" {
				query = `SELECT SUM(bytes) FROM traffic_hourly`
				want = 7
			}
			if err := s.db.QueryRow(query).Scan(&count); err != nil || count != want {
				t.Fatal("compaction changed totals", count, err)
			}
		})
	}
}

func TestRetentionDetailedBoundSaturationAndQueryValidation(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-time.Hour).UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 1025; i++ {
		if err := recordRetention(ctx, tx, "events", "time_expiry", 1, start, start, now.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	view, err := s.Retention(ctx, RetentionQuery{Start: now.Add(-2 * time.Hour), End: now, Limit: 100})
	if err != nil || len(view.Entries) != 100 || !view.More || view.EvictedEntries != 1 || len(view.Totals) != 1 || view.Totals[0].AffectedRows != 1025 {
		t.Fatal(view, err)
	}
	if _, err := s.db.Exec(`UPDATE retention_totals SET affected_rows=?,operations=?`, math.MaxInt64, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordRetention(ctx, tx, "events", "time_expiry", 7, start, start, now.AddDate(0, 0, 1024)); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if count := retentionCount(t, s, "events", "time_expiry"); count != math.MaxInt64 {
		t.Fatal("counter overflow", count)
	}
	for _, q := range []RetentionQuery{{Start: now, End: now, Limit: 1}, {Start: now, End: now.AddDate(0, 0, 401), Limit: 1}, {Start: now, End: now.Add(time.Hour), Dataset: "events; DROP", Limit: 1}, {Start: now, End: now.Add(time.Hour), Reason: "arbitrary", Limit: 1}, {Start: now, End: now.Add(time.Hour), BeforeID: -1, Limit: 1}} {
		if _, err := s.Retention(ctx, q); err == nil {
			t.Fatal("invalid query accepted", q)
		}
	}
}

func TestRetentionNotificationExpiryRollbackAndSaturation(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	e, m := alertFixture("expiry_outcome", "inc_expiry", "start")
	if err := s.InsertEventNotification(ctx, e, m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE notification_outbox SET expires_at=?`, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO notification_cooldowns VALUES ('telegram',?)`, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	failRetention(t, s)
	if err := s.ExpireNotifications(ctx, now); err == nil {
		t.Fatal("TTL committed without ledger")
	}
	var pending, expired, cooldowns int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT expired FROM notification_counters`).Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notification_cooldowns`).Scan(&cooldowns); err != nil {
		t.Fatal(err)
	}
	if pending != 1 || expired != 0 || cooldowns != 1 {
		t.Fatal("partial TTL commit", pending, expired, cooldowns)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_ledger`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE notification_counters SET expired=?`, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	if err := s.ExpireNotifications(ctx, now); err != nil {
		t.Fatal(err)
	}
	if retentionCount(t, s, "notification_outbox", "time_expiry") != 1 {
		t.Fatal("TTL outcome not accounted")
	}
	var total int64
	if err := s.db.QueryRow(`SELECT expired FROM notification_counters`).Scan(&total); err != nil || total != math.MaxInt64 {
		t.Fatal("TTL counter overflow", total, err)
	}
	var decision string
	if err := s.db.QueryRow(`SELECT decision FROM event_notifications`).Scan(&decision); err != nil || decision != "queued" {
		t.Fatal("enqueue decision removed", decision, err)
	}
}

func TestRetentionSilenceCapacityPreservesLeaseAndDecisions(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := s.db.Exec(`WITH RECURSIVE n(v) AS (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<10000) INSERT INTO notification_outbox(id,dedupe_key,destination,body,created_at,next_attempt,expires_at,suppressed_at,lease_until) SELECT 'old_'||v,'old_'||v,'telegram','',?,?,?, ?,CASE WHEN v=1 THEN ? ELSE NULL END FROM n`, now.Add(-time.Hour).UnixMilli(), now.UnixMilli(), now.Add(time.Hour).UnixMilli(), now.Add(-time.Minute).UnixMilli(), now.Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	e, m := alertFixture("silence_ledger", "inc_silence", "start")
	if err := s.InsertEventNotification(ctx, e, m); err != nil {
		t.Fatal(err)
	}
	failRetention(t, s)
	if _, err := s.AddSilence(ctx, Silence{Kind: e.Kind, ExpiresAt: now.Add(time.Hour)}, now); err == nil {
		t.Fatal("body cleared without ledger")
	}
	var body string
	if err := s.db.QueryRow(`SELECT body FROM notification_outbox WHERE id=?`, m.ID).Scan(&body); err != nil || body == "" {
		t.Fatal("body lost on rollback", err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_ledger`); err != nil {
		t.Fatal(err)
	}
	rule, err := s.AddSilence(ctx, Silence{Kind: e.Kind, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if retentionCount(t, s, "notification_outbox", "silence_body_discard") != 1 || retentionCount(t, s, "notification_outbox", "capacity_eviction") != 1 {
		t.Fatal("wrong silence accounting")
	}
	var rows, leased int
	if err := s.db.QueryRow(`SELECT COUNT(*),SUM(id='old_1') FROM notification_outbox`).Scan(&rows, &leased); err != nil || rows != 10000 || leased != 1 {
		t.Fatal("inflight lease evicted", rows, leased, err)
	}
	var decision string
	if err := s.db.QueryRow(`SELECT decision FROM event_notifications WHERE event_id=?`, e.ID).Scan(&decision); err != nil || decision != "silenced" {
		t.Fatal(decision, err)
	}
	if err := s.RemoveSilence(ctx, rule.Silence.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSilence(ctx, Silence{Kind: e.Kind, ExpiresAt: now.Add(time.Hour)}, now); err != nil {
		t.Fatal(err)
	}
	if retentionCount(t, s, "notification_silences", "capacity_eviction") != 1 {
		t.Fatal("revoked rule retirement missing")
	}
	if _, err := s.AddSilence(ctx, Silence{Kind: e.Kind, ExpiresAt: now.Add(3 * time.Hour)}, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if retentionCount(t, s, "notification_silences", "time_expiry") != 1 {
		t.Fatal("expired rule retirement missing")
	}
}

func TestRetentionLastWriteFailureAndMissingMetadataRollBack(t *testing.T) {
	for _, failure := range []string{"totals", "metadata"} {
		t.Run(failure, func(t *testing.T) {
			s := budgetStore(t)
			now := time.Now().UTC().Truncate(time.Hour)
			seedAuthDetail(t, s, now, 4)
			if failure == "totals" {
				if _, err := s.db.Exec(`CREATE TRIGGER refuse_total BEFORE INSERT ON retention_totals BEGIN SELECT RAISE(ABORT,'synthetic final ledger failure'); END`); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.db.Exec(`DELETE FROM retention_meta`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := compactAuthForTest(t, s); err == nil {
				t.Fatal("accounting failure ignored")
			}
			var rows, keys, gaps, ledger int
			for q, ptr := range map[string]*int{`SELECT COUNT(*) FROM auth_hourly`: &rows, `SELECT keys FROM auth_cardinality_hourly`: &keys, `SELECT COUNT(*) FROM coverage_gaps`: &gaps, `SELECT COUNT(*) FROM retention_ledger`: &ledger} {
				if err := s.db.QueryRow(q).Scan(ptr); err != nil {
					t.Fatal(err)
				}
			}
			if rows != 4 || keys != 4 || gaps != 0 || ledger != 0 {
				t.Fatal("compaction partially committed", rows, keys, gaps, ledger)
			}
		})
	}
}

func TestRetentionCoverageCapacityFailurePreservesOriginalRows(t *testing.T) {
	s := retentionStore(t)
	now := time.Now().UTC()
	seedGaps(t, s, now)
	failRetention(t, s)
	if err := s.RecordCoverageGap(context.Background(), CoverageGap{Name: "sensor_feed", Reason: "restart", Start: now, End: now}); err == nil {
		t.Fatal("capacity accounting error ignored")
	}
	var first, added int
	if err := s.db.QueryRow(`SELECT SUM(id=1),SUM(id=1001) FROM coverage_gaps`).Scan(&first, &added); err != nil || first != 1 || added != 0 {
		t.Fatal("gap capacity partially committed", first, added, err)
	}
}

// A coalesced day's final point remains relevant when it is exactly the query
// start. Gap/coverage entries can combine point records and are conservative;
// fixed hourly periods retain their exclusive right endpoint.
func TestRetentionCoalescedPointAtQueryStart(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, dataset := range []string{"events", "notification_outbox", "coverage_gaps", "coverage_intervals"} {
		for _, at := range []time.Time{now.Add(-time.Hour), now} {
			if err := recordRetention(ctx, tx, dataset, "time_expiry", 1, at.UnixMilli(), at.UnixMilli(), now); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := recordRetention(ctx, tx, "interface_hourly", "time_expiry", 1, now.Add(-time.Hour).UnixMilli(), now.UnixMilli(), now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, dataset := range []string{"events", "notification_outbox", "coverage_gaps", "coverage_intervals"} {
		view, err := s.Retention(ctx, RetentionQuery{Start: now, End: now.Add(time.Hour), Dataset: dataset, Limit: 10})
		if err != nil || len(view.Entries) != 1 || view.Entries[0].AffectedRows != 2 {
			t.Fatal("lost matching final point", dataset, view, err)
		}
		if len(view.Retained) != 1 || view.Retained[0].Rows != 0 {
			t.Fatal("ledger fabricated remaining rows", view)
		}
	}
	view, err := s.Retention(ctx, RetentionQuery{Start: now, End: now.Add(time.Hour), Dataset: "interface_hourly", Limit: 10})
	if err != nil || len(view.Entries) != 0 {
		t.Fatal("hourly exclusive endpoint changed", view, err)
	}
}

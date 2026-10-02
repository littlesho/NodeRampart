// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestDiagnosisRequiredUnknownDisabledAndOptionalDegradation(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	status := Status{GeneratedAt: now, Queue: &store.QueueStatus{}, ForeignKeys: &store.ForeignKeyStatus{}, Readiness: ReadinessStatus{ConfigurationLoaded: true, ConfigFingerprint: strings.Repeat("a", 64), StorageReady: true, InterfaceReady: true, BasicReady: true}}
	if d := DiagnoseAt(status, true, now); d.Code != 0 || d.State != "healthy" {
		t.Fatal("disabled components were treated as failure", d)
	}
	status.OptionalFailures = map[string]string{"telegram": "do-not-export-arbitrary-private-error"}
	if d := DiagnoseAt(status, true, now); d.Code != 1 || d.State != "degraded" || d.Checks[len(d.Checks)-1].Reason != "telegram_credentials_unavailable" {
		t.Fatal("optional degradation hidden or unbounded reason exposed", d)
	}
	status.ForeignKeys = nil
	if d := DiagnoseAt(status, true, now); d.Code != 2 || d.State != "unknown" {
		t.Fatal("unknown historical integrity called healthy", d)
	}
	status.ForeignKeys = &store.ForeignKeyStatus{Violations: []store.ForeignKeyViolation{{Table: "event_notifications", Parent: "events"}}}
	if d := DiagnoseAt(status, true, now); d.Code != 1 {
		t.Fatal("confirmed orphan hidden", d)
	}
	status.GeneratedAt = now.Add(-time.Minute - time.Nanosecond)
	if d := DiagnoseAt(status, false, now); d.Code != 2 {
		t.Fatal("stale status called current", d)
	}
}

func TestDiagnosisUsesLatestInterfaceCommitAndLegacyUnknown(t *testing.T) {
	now := time.Now().UTC()
	status := Status{GeneratedAt: now, SensorEnabled: true, Batches: 2, Queue: &store.QueueStatus{}, Readiness: ReadinessStatus{ConfigurationLoaded: true, ConfigFingerprint: strings.Repeat("a", 64), StorageReady: true, InterfaceReady: true, SensorReady: true}}
	status.Detection.ScanCoverageComplete = true
	status.Detection.UDPScanCoverageComplete = true
	if d := DiagnoseAt(status, false, now); d.Code != 2 {
		t.Fatal("legacy receipt invented commit confirmation", d)
	}
	status.SensorInterfaces = map[string]time.Time{"lo": now}
	status.SensorReceipts = map[string]SensorReceipt{"lo": {SessionID: strings.Repeat("b", 32), Sequence: 1}}
	status.SensorCommits = []store.SensorWatermark{{SessionID: strings.Repeat("a", 32), Sequence: 1, Interface: "lo", SentAt: now.Add(-time.Second), Complete: false}, {SessionID: strings.Repeat("b", 32), Sequence: 1, Interface: "lo", SentAt: now, Complete: true}}
	if d := DiagnoseAt(status, false, now); d.Code != 0 {
		t.Fatal("old partial commit poisoned new observation", d)
	}
	status.SensorCommits[1].Complete = false
	if d := DiagnoseAt(status, false, now); d.Code != 1 {
		t.Fatal("latest partial commit called healthy", d)
	}
	status.SensorCommitUnknown = true
	if d := DiagnoseAt(status, false, now); d.Code != 2 {
		t.Fatal("unreadable commit state called healthy", d)
	}
}

func TestDiagnosisAuthenticationWarmupIsUnknown(t *testing.T) {
	now := time.Now().UTC()
	status := Status{GeneratedAt: now, AuthEnabled: true, AuthWindowReadyAfter: now.Add(time.Minute), Queue: &store.QueueStatus{}, Readiness: ReadinessStatus{ConfigurationLoaded: true, ConfigFingerprint: strings.Repeat("a", 64), StorageReady: true, InterfaceReady: true, JournalReady: true, AuthDetectionReady: true}}
	if d := DiagnoseAt(status, false, now); d.Code != 2 {
		t.Fatal("cold authentication window called fully healthy", d)
	}
	status.AuthWindowReadyAfter = now.Add(-time.Second)
	if d := DiagnoseAt(status, false, now); d.Code != 0 {
		t.Fatal("completed warmup remained unknown", d)
	}
}

func sensorDiagnosisFixture(now time.Time) Status {
	status := Status{GeneratedAt: now, SensorEnabled: true, Batches: 1, Queue: &store.QueueStatus{}, Readiness: ReadinessStatus{ConfigurationLoaded: true, ConfigFingerprint: strings.Repeat("a", 64), StorageReady: true, InterfaceReady: true, SensorReady: true}}
	status.Detection.ScanCoverageComplete = true
	status.Detection.UDPScanCoverageComplete = true
	status.SensorInterfaces = map[string]time.Time{"lab0": now}
	status.SensorReceipts = map[string]SensorReceipt{"lab0": {SessionID: strings.Repeat("a", 32), Sequence: 1}}
	status.SensorCommits = []store.SensorWatermark{{SessionID: strings.Repeat("a", 32), Interface: "lab0", Sequence: 1, SentAt: time.UnixMicro(now.UnixMicro()).UTC(), Complete: true, EventsCommitted: true, NotificationsCommitted: true, Reason: "committed"}}
	return status
}

func assertSensorDiagnosis(t *testing.T, status Status, now time.Time, code int, reasons ...string) {
	t.Helper()
	diagnosis := DiagnoseAt(status, false, now)
	if diagnosis.Code != code {
		t.Fatalf("strict=%d want%d: %+v", diagnosis.Code, code, diagnosis.Checks)
	}
	got := map[string]bool{}
	for _, check := range diagnosis.Checks {
		if strings.HasPrefix(check.Reason, "sensor_commit_") {
			got[check.Reason] = true
		}
	}
	if len(got) != len(reasons) {
		t.Fatalf("sensor reasons=%v want%v", got, reasons)
	}
	for _, reason := range reasons {
		if !got[reason] {
			t.Fatalf("missing %s: %+v", reason, diagnosis)
		}
	}
}

func TestDiagnosisSensorReceiptPrecisionAndIdentity(t *testing.T) {
	aligned := time.Date(2026, 10, 3, 1, 0, 0, 123456000, time.UTC)
	for remainder := 0; remainder < 1000; remainder++ {
		status := sensorDiagnosisFixture(aligned.Add(time.Duration(remainder) * time.Nanosecond))
		t.Run(fmt.Sprintf("remainder%d", remainder), func(t *testing.T) { assertSensorDiagnosis(t, status, status.GeneratedAt, 0) })
	}
	for _, delta := range []time.Duration{time.Microsecond, 50 * time.Millisecond, 100 * time.Millisecond} {
		t.Run("newer-"+delta.String(), func(t *testing.T) {
			status := sensorDiagnosisFixture(aligned)
			status.SensorInterfaces["lab0"] = aligned.Add(delta)
			assertSensorDiagnosis(t, status, aligned, 2, "sensor_commit_unavailable")
		})
	}
	for _, tc := range []struct {
		name    string
		change  func(*Status)
		code    int
		reasons []string
	}{
		{"missing-watermark", func(s *Status) { s.SensorCommits = nil }, 2, []string{"sensor_commit_unavailable"}},
		{"unreadable", func(s *Status) { s.SensorCommitUnknown = true }, 2, []string{"sensor_commit_unknown"}},
		{"missing-identity-old-daemon", func(s *Status) { s.SensorReceipts = nil }, 2, []string{"sensor_commit_unavailable"}},
		{"legacy-identity", func(s *Status) { s.SensorReceipts["lab0"] = SensorReceipt{} }, 2, []string{"sensor_commit_unavailable"}},
		{"next-sequence-same-time", func(s *Status) {
			s.SensorReceipts["lab0"] = SensorReceipt{SessionID: strings.Repeat("a", 32), Sequence: 2}
		}, 2, []string{"sensor_commit_unavailable"}},
		{"new-session-same-time", func(s *Status) {
			s.SensorReceipts["lab0"] = SensorReceipt{SessionID: strings.Repeat("b", 32), Sequence: 1}
		}, 2, []string{"sensor_commit_unavailable"}},
		{"watermark-advanced-does-not-prove-old-receipt", func(s *Status) {
			s.SensorCommits[0].Sequence = 3
			s.SensorCommits[0].SentAt = s.SensorCommits[0].SentAt.Add(time.Second)
		}, 2, []string{"sensor_commit_unavailable"}},
		{"other-interface-missing", func(s *Status) {
			s.SensorInterfaces["lab1"] = aligned
			s.SensorReceipts["lab1"] = s.SensorReceipts["lab0"]
		}, 2, []string{"sensor_commit_unavailable"}},
		{"other-interface-pending", func(s *Status) {
			s.SensorInterfaces["lab1"] = aligned.Add(100 * time.Millisecond)
			s.SensorReceipts["lab1"] = SensorReceipt{SessionID: strings.Repeat("a", 32), Sequence: 2}
			w := s.SensorCommits[0]
			w.Interface = "lab1"
			s.SensorCommits = append(s.SensorCommits, w)
		}, 2, []string{"sensor_commit_unavailable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sensorDiagnosisFixture(aligned.Add(789 * time.Nanosecond))
			tc.change(&s)
			assertSensorDiagnosis(t, s, s.GeneratedAt, tc.code, tc.reasons...)
		})
	}
	for _, reason := range []string{"derived_events_pending", "derived_events_rejected", "notification_rejected"} {
		t.Run(reason, func(t *testing.T) {
			s := sensorDiagnosisFixture(aligned.Add(789 * time.Nanosecond))
			s.SensorCommits[0].Complete = false
			s.SensorCommits[0].Reason = reason
			s.SensorCommits[0].NotificationsCommitted = false
			s.SensorCommits[0].EventsCommitted = reason == "notification_rejected"
			assertSensorDiagnosis(t, s, s.GeneratedAt, 1, "sensor_commit_partial")
			s.SensorInterfaces["lab1"] = aligned
			assertSensorDiagnosis(t, s, s.GeneratedAt, 2, "sensor_commit_unavailable", "sensor_commit_partial")
		})
	}
}

func TestDiagnosisSensorStoreRoundTripAndPending(t *testing.T) {
	a := eventTestApp(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 3, 1, 0, 0, 123456789, time.UTC)
	batch := protocol.Batch{ProtocolVersion: protocol.Version, SessionID: strings.Repeat("a", 32), Sequence: 1, Interface: "lab0", SentAt: at, IntervalMillis: 100}
	status := sensorDiagnosisFixture(at)
	commit := func(b protocol.Batch) {
		t.Helper()
		ack, err := a.options.Store.CommitSensorBatch(ctx, b, nil, true, true, true, "committed")
		if err != nil || !ack.Complete {
			t.Fatal(ack, err)
		}
		w, exists, err := a.options.Store.SensorWatermark(ctx, b.SessionID, b.Interface)
		if err != nil || !exists || !w.SentAt.Equal(time.UnixMicro(b.SentAt.UnixMicro())) || w.SentAt.Equal(b.SentAt) {
			t.Fatal("real store quantization not exercised", w, exists, err)
		}
		status.SensorCommits, err = a.options.Store.SensorWatermarks(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	commit(batch)
	assertSensorDiagnosis(t, status, at, 0)
	// The actual status JSON carries the new identity to CLI-side re-diagnosis.
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Status
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	assertSensorDiagnosis(t, decoded, at, 0)
	next := batch
	next.Sequence++
	next.SentAt = at.Add(100 * time.Millisecond)
	status.SensorInterfaces["lab0"] = next.SentAt
	status.SensorReceipts["lab0"] = SensorReceipt{SessionID: next.SessionID, Sequence: next.Sequence}
	assertSensorDiagnosis(t, status, at, 2, "sensor_commit_unavailable")
	commit(next)
	assertSensorDiagnosis(t, status, at, 0)
	next.SessionID = strings.Repeat("b", 32)
	next.Sequence = 1
	next.SentAt = next.SentAt.Add(100 * time.Millisecond)
	status.SensorInterfaces["lab0"] = next.SentAt
	status.SensorReceipts["lab0"] = SensorReceipt{SessionID: next.SessionID, Sequence: next.Sequence}
	assertSensorDiagnosis(t, status, at, 2, "sensor_commit_unavailable")
	commit(next)
	assertSensorDiagnosis(t, status, at, 0)
	// A later observation never rewrites the previously captured unknown snapshot.
	assertSensorDiagnosis(t, decoded, at, 0)
	status.SensorReceipts["lab0"] = SensorReceipt{SessionID: next.SessionID, Sequence: 2}
	status.SensorInterfaces["lab0"] = next.SentAt.Add(100 * time.Millisecond)
	pendingData, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	next.Sequence = 3
	next.SentAt = next.SentAt.Add(200 * time.Millisecond)
	commit(next) // sequence 2 was never committed, even though 3 is complete.
	assertSensorDiagnosis(t, status, at, 2, "sensor_commit_unavailable")
	var historical Status
	if err := json.Unmarshal(pendingData, &historical); err != nil {
		t.Fatal(err)
	}
	assertSensorDiagnosis(t, historical, at, 2, "sensor_commit_unavailable")
}

func TestSensorReceiptTrackingBoundedAndSnapshot(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Sensor.Interface = ""
	a.options.Config.Sensor.Interfaces = []string{"lab0", "lab1"}
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		a.mu.Lock()
		a.recordSensorReceiptLocked(protocol.Batch{ProtocolVersion: 5, SessionID: strings.Repeat("a", 32), Sequence: uint64(i + 1), Interface: fmt.Sprintf("lab%d", i), SentAt: now.Add(time.Duration(i) * time.Millisecond)})
		a.mu.Unlock()
	}
	if len(a.sensorByInterface) != 2 || len(a.sensorReceipts) != 2 {
		t.Fatal("receipt maps unbounded")
	}
	if _, ok := a.sensorReceipts["lab0"]; ok {
		t.Fatal("evicted timestamp retained identity")
	}
	status := a.Status(context.Background())
	if status.SensorReceipts["lab1"] != a.sensorReceipts["lab1"] || !status.SensorInterfaces["lab1"].Equal(a.sensorByInterface["lab1"]) {
		t.Fatal("status lost receipt pair")
	}
	status.SensorReceipts["lab1"] = SensorReceipt{}
	if a.sensorReceipts["lab1"].Sequence != 2 {
		t.Fatal("status aliases live identity map")
	}
	a.mu.Lock()
	a.recordSensorReceiptLocked(protocol.Batch{ProtocolVersion: 4, Interface: "lab1", SentAt: now})
	a.mu.Unlock()
	if a.sensorReceipts["lab1"] != (SensorReceipt{}) {
		t.Fatal("legacy inherited v5 identity")
	}
	if err := a.updateInterfaces(context.Background(), []collector.InterfaceObservation{{Name: "lab1", Index: 2}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.sensorReceipts["lab1"]; ok {
		t.Fatal("replaced interface retained identity")
	}
}

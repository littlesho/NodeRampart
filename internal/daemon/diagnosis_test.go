// SPDX-License-Identifier: MIT

package daemon

import (
	"strings"
	"testing"
	"time"

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
	status.SensorCommits = []store.SensorWatermark{{Interface: "lo", SentAt: now.Add(-time.Second), Complete: false}, {Interface: "lo", SentAt: now, Complete: true}}
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

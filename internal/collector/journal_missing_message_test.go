// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func missingMessageRecord(t *testing.T, at time.Time, kind string) string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(reliableRecord(t, "message-"+kind, at)), &fields); err != nil {
		t.Fatal(err)
	}
	switch kind {
	case "absent":
		delete(fields, "MESSAGE")
	case "null":
		fields["MESSAGE"] = json.RawMessage("null")
	case "empty":
		fields["MESSAGE"] = json.RawMessage(`""`)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestJournalMissingMessageDoesNotCountAsTrusted(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	for _, kind := range []string{"absent", "null", "empty"} {
		t.Run(kind, func(t *testing.T) {
			entry, ok := decodeJournalEntry([]byte(missingMessageRecord(t, at, kind)), at)
			want := "malformed_record"
			if kind == "empty" {
				want = "unrecognized_message"
			}
			if !ok || entry.Cursor != "message-"+kind || entry.Observation != nil || entry.SkipReason != want {
				t.Fatalf("missing message was treated as trusted evidence: entry=%+v ok=%v want=%s", entry, ok, want)
			}
		})
	}
}

func TestReliableJournalMissingMessageCannotRecover(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	for _, kind := range []string{"absent", "null", "empty"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			path := journalScript(t, "cat <<'RECORD'\n"+missingMessageRecord(t, at, kind)+"\nRECORD\nexec sleep 30\n")
			consumed, degraded, recovered := 0, 0, 0
			var last JournalStatus
			err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{
				InitialObservedAt: at.Add(-time.Second), InitialRecoveryPending: true,
				OnDegradation: func(context.Context, JournalStatus) error { degraded++; return nil },
				OnStatus: func(status JournalStatus) {
					last = status
					if status.Reason == "record_persisted" {
						recovered++
					}
				},
			}, func(context.Context, JournalEntry) error {
				consumed++
				time.AfterFunc(20*time.Millisecond, cancel)
				return nil
			})
			wantDegraded, wantRecovered, wantState := 1, 0, "degraded"
			if kind == "empty" {
				wantDegraded, wantRecovered, wantState = 0, 1, "running"
			}
			if err != nil || consumed != 1 || degraded != wantDegraded || recovered != wantRecovered || last.State != wantState {
				t.Fatalf("missing message changed recovery: consumed=%d degraded=%d recovered=%d last=%+v err=%v", consumed, degraded, recovered, last, err)
			}
		})
	}
}

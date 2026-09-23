// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
)

func TestJournalOlderAcknowledgedCursorDoesNotRecoverIngest(t *testing.T) {
	a := eventTestApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	at := time.Now().UTC().Add(-time.Second)
	for _, cursor := range []string{"older-trusted", "latest-trusted"} {
		if err := a.handleJournal(ctx, collector.JournalEntry{Cursor: cursor, ReceivedAt: at, ObservedAt: at, SkipReason: "unrecognized_message"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.recordJournalDegradation(ctx, collector.JournalStatus{State: "gap", Reason: "malformed_record", Since: at, At: at, Count: 1}); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := a.options.Store.JournalCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stream := ""
	for _, cursor := range []string{"latest-trusted", "older-trusted"} {
		data, err := json.Marshal(map[string]string{"__CURSOR": cursor, "__REALTIME_TIMESTAMP": fmt.Sprint(at.UnixMicro()), "MESSAGE": "pam_unix(sshd:session): session closed for user lab-user", "_UID": "0", "_EXE": "/usr/sbin/sshd", "_SYSTEMD_UNIT": "ssh.service", "_TRANSPORT": "syslog"})
		if err != nil {
			t.Fatal(err)
		}
		stream += string(data) + "\n"
	}
	path := filepath.Join(t.TempDir(), "journalctl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat <<'DATA'\n"+stream+"DATA\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	consumed, recovered := 0, 0
	err = (collector.Journal{Path: path}).RunReliable(ctx, collector.JournalOptions{InitialCursor: checkpoint.Cursor, InitialObservedAt: checkpoint.ObservedAt, InitialRecoveryPending: checkpoint.RecoveryPending, OnDegradation: a.recordJournalDegradation, OnStatus: func(s collector.JournalStatus) {
		if s.Reason == "process_started" && s.State != "starting" {
			time.AfterFunc(100*time.Millisecond, cancel)
		}
		if s.State == "running" {
			recovered++
			cancel()
		}
	}}, func(ctx context.Context, entry collector.JournalEntry) error {
		consumed++
		return a.handleJournal(ctx, entry)
	})
	after, readErr := a.options.Store.JournalCheckpoint(context.Background())
	if err != nil || readErr != nil || consumed != 1 || recovered != 0 || !after.RecoveryPending {
		t.Fatalf("old durable cursor faked ingest recovery: consumed=%d recovered=%d pending=%v err=%v/%v", consumed, recovered, after.RecoveryPending, err, readErr)
	}
}

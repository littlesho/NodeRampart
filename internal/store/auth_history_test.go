// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSSHLoginHistoryBoundAndCancellation(t *testing.T) {
	s := budgetStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= MaxSSHHistoryLogins; i++ {
		if _, err := tx.Exec(`INSERT INTO events(id,observed_at,kind,severity,summary,source_range) VALUES(?,?,'ssh_login_success','info','synthetic','192.0.2.0/24')`, fmt.Sprintf("history-%04d", i), now.Add(-time.Duration(i+1)*time.Minute).UnixMilli()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	entries, truncated, err := s.SSHLoginHistory(context.Background(), now.Add(-24*time.Hour), now)
	if err != nil || !truncated || len(entries) != MaxSSHHistoryLogins {
		t.Fatal(len(entries), truncated, err)
	}
	for _, entry := range entries {
		if entry.SourceIP != "" || entry.SourceRange != "192.0.2.0/24" {
			t.Fatal("historical prefix reconstructed raw address", entry)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.SSHLoginHistory(ctx, now.Add(-24*time.Hour), now); err == nil {
		t.Fatal("canceled history query reported healthy")
	}
}

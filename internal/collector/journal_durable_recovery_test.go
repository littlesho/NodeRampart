// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReliableJournalDegradationRequiresDurableAcknowledgement(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	for name, fault := range recoveryFaults(t, at) {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			acknowledgements, consumed := 0, 0
			failed := false
			path := journalScript(t, "cat <<'FAULT'\n"+fault+"\nFAULT\nexec sleep 30\n")
			err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{
				InitialObservedAt: at.Add(-time.Second),
				OnDegradation: func(context.Context, JournalStatus) error {
					acknowledgements++
					return errors.New("synthetic marker persistence failure")
				},
				OnStatus: func(status JournalStatus) {
					if status.Reason == "persist_failed" {
						failed = true
						cancel()
					}
					if status.Reason == "record_persisted" {
						t.Error("failed marker write claimed trusted persistence")
					}
				},
			}, func(context.Context, JournalEntry) error { consumed++; return nil })
			if err != nil || !failed || acknowledgements != 1 || consumed != 0 {
				t.Fatalf("unacknowledged degradation advanced consumption: ack=%d consumed=%d failed=%v err=%v", acknowledgements, consumed, failed, err)
			}
		})
	}
}

func TestReliableJournalInitialRecoveryRequiresNewTrustedAcknowledgement(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	closeRecord := strings.ReplaceAll(reliableRecord(t, "trusted", at), "Failed password for root from 192.0.2.7 port 54321 ssh2", "pam_unix(sshd:session): session closed for user lab-user")
	for _, scenario := range []string{"quiet", "trusted", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			body := ""
			if scenario != "quiet" {
				body = "cat <<'RECORD'\n" + closeRecord + "\nRECORD\n"
			}
			if scenario == "duplicate" {
				body += "cat <<'RECORD'\nmalformed JSON\n" + closeRecord + "\nRECORD\n"
			}
			path := journalScript(t, body+"exec sleep 30\n")
			consumed, recovered := 0, 0
			var last JournalStatus
			err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{
				InitialObservedAt: at.Add(-time.Second), InitialRecoveryPending: scenario != "duplicate",
				OnStatus: func(status JournalStatus) {
					last = status
					if status.Reason == "record_persisted" {
						recovered++
						cancel()
					}
					if scenario == "quiet" && status.State != "starting" && status.Reason == "process_started" {
						if status.State == "running" {
							t.Error("restored pending cleared by child startup")
						}
						time.AfterFunc(50*time.Millisecond, cancel)
					}
					if scenario == "duplicate" && status.QualityDegraded() {
						time.AfterFunc(50*time.Millisecond, cancel)
					}
				},
			}, func(_ context.Context, entry JournalEntry) error {
				consumed++
				if entry.Observation != nil || entry.SkipReason != "unrecognized_message" {
					t.Error("close record counted as authentication")
				}
				return nil
			})
			wantConsumed, wantRecovered := 1, 0
			if scenario == "quiet" {
				wantConsumed = 0
			}
			if scenario == "trusted" {
				wantRecovered = 1
			}
			if err != nil || consumed != wantConsumed || recovered != wantRecovered || scenario != "trusted" && last.State == "running" {
				t.Fatalf("readiness without a new durable ack: consumed=%d recovered=%d last=%+v err=%v", consumed, recovered, last, err)
			}
		})
	}
}

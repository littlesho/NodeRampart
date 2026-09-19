// SPDX-License-Identifier: MIT

package detect

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
)

func TestAuthCountsFinalOutcomesRatherThanLogLines(t *testing.T) {
	for _, lines := range [][]string{
		{
			"Invalid user demo from 192.0.2.7 port 54321",
			"pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=192.0.2.7  user=demo",
			"Failed password for invalid user demo from 192.0.2.7 port 54321 ssh2",
		},
		{
			"Failed password for invalid user demo from 192.0.2.7 port 54321 ssh2",
			"pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=192.0.2.7  user=demo",
			"Invalid user demo from 192.0.2.7 port 54321",
		},
	} {
		cfg := config.Defaults().Auth
		cfg.Threshold = 2
		detector := NewAuth(cfg)
		now := time.Now().UTC()
		for i, line := range lines {
			observation, ok := collector.ParseSSH(line, now.Add(time.Duration(i)*time.Millisecond))
			if !ok {
				t.Fatalf("fixture failed to parse: %q", line)
			}
			if event := detector.Observe(observation); event != nil {
				t.Fatalf("one attempt crossed two-attempt threshold: %#v", event)
			}
		}
		// A second failed password in the same session is a separate attempt,
		// even with the same source port, username and coarse timestamp.
		observation, _ := collector.ParseSSH("Failed password for invalid user demo from 192.0.2.7 port 54321 ssh2", now.Add(3*time.Millisecond))
		event := detector.Observe(observation)
		if event == nil || event.Count != 2 || event.Evidence["invalid_user"] != "true" || event.Evidence["count_basis"] != "openssh_final_failure" {
			t.Fatalf("expected two canonical failures: %#v", event)
		}
	}
}

func TestAuthPrunesBeforeSaturatedAdmission(t *testing.T) {
	cfg := config.Defaults().Auth
	cfg.Threshold = 1
	detector := NewAuth(cfg)
	now := time.Now().UTC()
	stale := now.Add(-cfg.Window.Duration - cfg.Cooldown.Duration - time.Second)
	for i := 0; i < maxAuthSources; i++ {
		source := fmt.Sprint(i)
		detector.failures[source] = []time.Time{stale}
		detector.lastSeen[source] = stale
	}
	event := detector.Observe(collector.AuthObservation{ObservedAt: now, Kind: collector.AuthFailure, Method: "password", SourceIP: netip.MustParseAddr("192.0.2.7")})
	if event == nil || event.Count != 1 || len(detector.failures) != 1 || detector.Stats().RejectedSources != 0 {
		t.Fatalf("expired full table rejected new source: event=%#v states=%d stats=%+v", event, len(detector.failures), detector.Stats())
	}
}

func TestAuthSuccessIncludesOnlyPrecedingWindowFailures(t *testing.T) {
	cfg := config.Defaults().Auth
	detector := NewAuth(cfg)
	now := time.Now().UTC()
	detector.failures["192.0.2.7"] = []time.Time{now.Add(-2 * cfg.Window.Duration), now.Add(-time.Second), now.Add(time.Second)}
	observation, ok := collector.ParseSSH("Accepted password for bob from 192.0.2.7 port 22 ssh2", now)
	if !ok {
		t.Fatal("fixture did not parse")
	}
	event := detector.Observe(observation)
	if event == nil || event.Evidence["preceding_source_failures"] != "1" {
		t.Fatalf("incorrect preceding failure evidence: %#v", event)
	}
}

func TestAuthAuxiliaryObservationsDoNotAllocateFailureState(t *testing.T) {
	detector := NewAuth(config.Defaults().Auth)
	for _, line := range []string{
		"Invalid user demo from 192.0.2.7 port 54321",
		"pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=192.0.2.7  user=demo",
	} {
		observation, ok := collector.ParseSSH(line, time.Now())
		if !ok {
			t.Fatalf("fixture failed to parse: %q", line)
		}
		if event := detector.Observe(observation); event != nil {
			t.Fatalf("auxiliary observation generated event: %#v", event)
		}
	}
	if len(detector.failures) != 0 || len(detector.lastSeen) != 0 || len(detector.lastAlert) != 0 {
		t.Fatal("auxiliary log lines consumed authentication source state")
	}
}

// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestJournalCollectsOpenSSHProcessIdentifiers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("journalctl fixture requires a POSIX shell")
	}
	for _, identifier := range []string{"sshd", "sshd-session", "sshd-auth"} {
		t.Run(identifier, func(t *testing.T) {
			// Model journalctl selecting an existing record only when the
			// corresponding identifier is requested. No system journal is read.
			fixture := fmt.Sprintf(`#!/bin/sh
for argument do
  if [ "$argument" = "--identifier=%s" ]; then
    printf '%%s\n' '{"MESSAGE":"Accepted publickey for lab-user from 192.0.2.23 port 42000 ssh2","_SOURCE_REALTIME_TIMESTAMP":"1788912000000000","_UID":"0","_EXE":"/usr/lib/openssh/%s","_SYSTEMD_UNIT":"ssh.service","_TRANSPORT":"syslog"}'
    exit 0
  fi
done
`, identifier, identifier)
			if identifier == "sshd" {
				fixture = strings.ReplaceAll(fixture, "/usr/lib/openssh/sshd\"", "/usr/sbin/sshd\"")
			}
			path := filepath.Join(t.TempDir(), "journalctl")
			if err := os.WriteFile(path, []byte(fixture), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			observations := make(chan AuthObservation, 1)
			if err := (Journal{Path: path}).Run(ctx, observations); err != nil {
				t.Fatal(err)
			}
			if len(observations) != 1 {
				t.Fatalf("SSH observation from %s was not collected", identifier)
			}
			got := <-observations
			if got.Kind != AuthSuccess || got.User != "lab-user" || got.Method != "publickey" || got.SourceIP.String() != "192.0.2.23" || !got.ObservedAt.Equal(time.UnixMicro(1788912000000000)) {
				t.Fatalf("unexpected SSH observation: %#v", got)
			}
		})
	}
}

func TestJournalTrustedOrigin(t *testing.T) {
	trusted := journalRecord{UID: "0", Executable: "/usr/sbin/sshd", Unit: "ssh.service", Transport: "syslog"}
	for _, executable := range []string{"/usr/sbin/sshd", "/usr/bin/sshd", "/usr/lib/openssh/sshd-session", "/usr/lib/openssh/sshd-auth", "/usr/libexec/openssh/sshd-session", "/usr/libexec/openssh/sshd-auth"} {
		for _, unit := range []string{"ssh.service", "sshd.service", "ssh@lab.service", "sshd@1-192.0.2.1:22-192.0.2.2:23.service"} {
			record := trusted
			record.Executable, record.Unit = executable, unit
			if !record.trustedSSHOrigin() {
				t.Errorf("rejected SSH origin: %#v", record)
			}
		}
	}
	for _, change := range []func(*journalRecord){
		func(r *journalRecord) { r.UID = "1000" },
		func(r *journalRecord) { r.UID = "" },
		func(r *journalRecord) { r.Executable = "/usr/bin/logger" },
		func(r *journalRecord) { r.Executable = "/tmp/sshd" },
		func(r *journalRecord) { r.Executable = "/usr/sbin/sshd (deleted)" },
		func(r *journalRecord) { r.Executable = "" },
		func(r *journalRecord) { r.Unit = "user@1000.service" },
		func(r *journalRecord) { r.Unit = "ssh@.service" },
		func(r *journalRecord) { r.Unit = "sshd.service.bad" },
		func(r *journalRecord) { r.Unit = "" },
		func(r *journalRecord) { r.UserUnit = "sshd.service" },
		func(r *journalRecord) { r.Transport = "stdout" },
		func(r *journalRecord) { r.Transport = "" },
	} {
		record := trusted
		change(&record)
		if record.trustedSSHOrigin() {
			t.Errorf("accepted untrusted origin: %#v", record)
		}
	}
}

func TestJournalTimestampFallback(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, test := range []struct {
		source, received string
		want             time.Time
	}{
		{"1788912000000000", "1788912001000000", time.UnixMicro(1788912000000000)},
		{"", "1788912001000000", time.UnixMicro(1788912001000000)},
		{"bad", "1788912001000000", time.UnixMicro(1788912001000000)},
		{"-1", "1788912001000000", time.UnixMicro(1788912001000000)},
		{"0", "1788912001000000", time.UnixMicro(1788912001000000)},
		{"9223372036854775808", "1788912001000000", time.UnixMicro(1788912001000000)},
		{"", "bad", now},
	} {
		record := journalRecord{Realtime: test.source, ReceivedRealtime: test.received}
		if got := record.observedAt(now); !got.Equal(test.want) {
			t.Errorf("timestamp source=%q received=%q: got %s want %s", test.source, test.received, got, test.want)
		}
	}
}

func TestJournalRejectsForgedIdentifierAndUsesReceptionTime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("journalctl fixture requires a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "journalctl")
	fixture := `#!/bin/sh
cat <<'JOURNAL'
{"MESSAGE":"Accepted publickey for root from 192.0.2.23 port 42000 ssh2","SYSLOG_IDENTIFIER":"sshd","_UID":"1000","_EXE":"/usr/bin/logger","_SYSTEMD_USER_UNIT":"synthetic.service","_TRANSPORT":"syslog","__REALTIME_TIMESTAMP":"1788912000000000"}
{"MESSAGE":"Failed password for bob from 192.0.2.23 port 42000 ssh2","SYSLOG_IDENTIFIER":"sshd-session","_UID":"0","_EXE":"/usr/libexec/openssh/sshd-session","_SYSTEMD_UNIT":"sshd.service","_TRANSPORT":"syslog","__REALTIME_TIMESTAMP":"1788912000000000"}
JOURNAL
`
	if err := os.WriteFile(path, []byte(fixture), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output := make(chan AuthObservation, 2)
	if err := (Journal{Path: path}).Run(ctx, output); err != nil {
		t.Fatal(err)
	}
	if len(output) != 1 {
		t.Fatalf("expected only trusted record, got %d records", len(output))
	}
	got := <-output
	if got.Kind != AuthFailure || got.User != "bob" || !got.ObservedAt.Equal(time.UnixMicro(1788912000000000)) {
		t.Fatalf("incorrect trusted observation: %#v", got)
	}
}

func FuzzJournalRecord(f *testing.F) {
	f.Add([]byte(`{"MESSAGE":"Failed password for bob from 192.0.2.1 port 22 ssh2","_UID":"0","_EXE":"/usr/sbin/sshd","_SYSTEMD_UNIT":"ssh.service","_TRANSPORT":"syslog","__CURSOR":"synthetic-cursor","__REALTIME_TIMESTAMP":"1788912000000000"}`))
	f.Add([]byte(`{"_UID":["0","1000"],"MESSAGE":[65,66]}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 256<<10 {
			return
		}
		var record journalRecord
		if json.Unmarshal(raw, &record) == nil && record.trustedSSHOrigin() {
			_, _ = ParseSSH(record.Message, record.observedAt(time.Unix(1, 0)))
		}
		if entry, ok := decodeJournalEntry(raw, time.Unix(1, 0)); ok && (!validJournalCursor(entry.Cursor) || entry.ReceivedAt.IsZero()) {
			t.Fatalf("invalid checkpoint escaped decoding: %#v", entry)
		}
	})
}

// SPDX-License-Identifier: MIT

package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Journal struct{ Path string }

func journalSelectionArguments() []string {
	// Narrow the subprocess stream with trusted credential/executable matches
	// before decoding; local users cannot consume replay budget by tagging
	// arbitrary logger messages as sshd. Decoder validation remains mandatory.
	return []string{
		"--identifier=sshd", "--identifier=sshd-session", "--identifier=sshd-auth",
		"--output-fields=MESSAGE,_SOURCE_REALTIME_TIMESTAMP,_UID,_EXE,_SYSTEMD_UNIT,_SYSTEMD_USER_UNIT,_TRANSPORT",
		"_UID=0", "_EXE=/usr/sbin/sshd", "_EXE=/usr/bin/sshd",
		"_EXE=/usr/lib/openssh/sshd-session", "_EXE=/usr/lib/openssh/sshd-auth",
		"_EXE=/usr/libexec/openssh/sshd-session", "_EXE=/usr/libexec/openssh/sshd-auth",
	}
}

type journalRecord struct {
	Cursor           string `json:"__CURSOR"`
	Message          string `json:"MESSAGE"`
	Realtime         string `json:"_SOURCE_REALTIME_TIMESTAMP"`
	ReceivedRealtime string `json:"__REALTIME_TIMESTAMP"`
	UID              string `json:"_UID"`
	Executable       string `json:"_EXE"`
	Unit             string `json:"_SYSTEMD_UNIT"`
	UserUnit         string `json:"_SYSTEMD_USER_UNIT"`
	Transport        string `json:"_TRANSPORT"`
}

func (r journalRecord) trustedSSHOrigin() bool {
	// SYSLOG_IDENTIFIER and _COMM are selection/display fields, not proof of
	// origin. Require journald's sender credentials, executable path and a
	// system service or logind session scope. A scope name alone proves no SSH
	// identity. Reject inherited stdout credentials and user services.
	if r.UID != "0" || r.UserUnit != "" || (r.Transport != "syslog" && r.Transport != "journal") {
		return false
	}
	if !sshUnit(r.Unit) && !loginSessionScope(r.Unit) {
		return false
	}
	// Debian 13 uses /usr/{sbin,lib/openssh}; Fedora 44 has merged sbin
	// into bin and installs its split SSH executables in libexec/openssh.
	switch r.Executable {
	case "/usr/sbin/sshd", "/usr/bin/sshd",
		"/usr/lib/openssh/sshd-session", "/usr/lib/openssh/sshd-auth",
		"/usr/libexec/openssh/sshd-session", "/usr/libexec/openssh/sshd-auth":
		return true
	}
	return false
}

func sshUnit(unit string) bool {
	if unit == "ssh.service" || unit == "sshd.service" {
		return true
	}
	// Socket activation assigns one system service instance per connection.
	for _, prefix := range []string{"ssh@", "sshd@"} {
		if strings.HasPrefix(unit, prefix) && strings.HasSuffix(unit, ".service") && len(unit) > len(prefix)+len(".service") && len(unit) <= 256 && !strings.ContainsAny(unit, "/\r\n\x00") {
			return true
		}
	}
	return false
}

func loginSessionScope(unit string) bool {
	// logind v257 uses a decimal uint32 audit session ID or a c-prefixed
	// uint64 counter, without leading zeroes. Audit IDs exclude 0 and
	// UINT32_MAX; that sentinel does not apply to the independent counter.
	// Bound length before parsing.
	// This only checks unit shape; trustedSSHOrigin must check the sender.
	if len(unit) > len("session-c")+20+len(".scope") || !strings.HasPrefix(unit, "session-") || !strings.HasSuffix(unit, ".scope") {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(unit, "session-"), ".scope")
	bits := 32
	if strings.HasPrefix(id, "c") {
		id = strings.TrimPrefix(id, "c")
		bits = 64
	}
	if len(id) == 0 || id[0] < '1' || id[0] > '9' {
		return false
	}
	number, err := strconv.ParseUint(id, 10, bits)
	return err == nil && (bits != 32 || number != 1<<32-1)
}

func (r journalRecord) observedAt(now time.Time) time.Time {
	// Prefer the earliest journal-trusted source timestamp, then journal
	// reception time. Processing time is only a last resort for malformed data.
	for _, value := range []string{r.Realtime, r.ReceivedRealtime} {
		if microseconds, err := strconv.ParseInt(value, 10, 64); err == nil && microseconds > 0 {
			return time.UnixMicro(microseconds).UTC()
		}
	}
	return now.UTC()
}

func (j Journal) Run(ctx context.Context, output chan<- AuthObservation) error {
	// Recent OpenSSH versions split authentication/session logging into child
	// executables. Repeated identifiers are alternatives for the same field.
	args := append([]string{"--follow", "--lines=0", "--output=json", "--no-pager"}, journalSelectionArguments()...)
	command := exec.CommandContext(ctx, j.Path, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start journalctl: %w", err)
	}
	// Every exit path, including a full output channel and malformed oversized
	// input, must reap the child instead of leaving journalctl running.
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 256<<10)
	for scanner.Scan() {
		var record journalRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if !record.trustedSSHOrigin() {
			continue
		}
		observation, ok := ParseSSH(record.Message, record.observedAt(time.Now()))
		if !ok {
			continue
		}
		select {
		case output <- observation:
		case <-ctx.Done():
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read journalctl: %w", err)
	}
	if err := command.Wait(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("journalctl exited: %w", err)
	}
	return nil
}

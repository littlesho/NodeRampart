// SPDX-License-Identifier: MIT

package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type JournalOptions struct {
	InitialCursor          string
	InitialObservedAt      time.Time // Journal reception time stored with the checkpoint.
	BackfillWindow         time.Duration
	MaxBackfill            int
	RestartMin             time.Duration
	RestartMax             time.Duration
	OnStatus               func(JournalStatus)
	InitialRecoveryPending bool
	// OnDegradation must durably record current record-quality loss before
	// consumption can advance the checkpoint. Other gap kinds are historical.
	OnDegradation func(context.Context, JournalStatus) error
}

// JournalEntry is delivered serially. A nil Observation still represents a
// journal entry that the consumer must checkpoint, optionally recording its
// SkipReason. Cursor and ReceivedAt are journal-assigned addressing metadata.
type JournalEntry struct {
	Cursor      string
	ObservedAt  time.Time
	ReceivedAt  time.Time
	Observation *AuthObservation
	SkipReason  string
}

type JournalStatus struct {
	State  string    `json:"state"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at_utc"`
	Since  time.Time `json:"since_utc"`
	Count  uint64    `json:"count"`
}

func (s JournalStatus) QualityDegraded() bool {
	return s.State == "degraded" || s.State == "gap" && s.Reason == "malformed_record"
}

type journalAttemptError struct{ reason string }

// ErrJournalAlreadyAcknowledged reports a successful duplicate checkpoint,
// not a new trusted ingest. It advances replay without clearing recovery.
var ErrJournalAlreadyAcknowledged = errors.New("journal entry already acknowledged")

func (e journalAttemptError) Error() string { return "journal: " + e.reason }

// RunReliable restarts journalctl with bounded backoff and backfill. consume's
// return is the acknowledgement: it must atomically persist its aggregate,
// events/outbox and checkpoint, and defensively deduplicate cursors. Return
// ErrJournalAlreadyAcknowledged for a duplicate durable checkpoint; it is not
// recovery evidence. Other errors leave the in-memory cursor unchanged. The caller
// must retain any prepared detector result across retries of the same cursor.
func (j Journal) RunReliable(ctx context.Context, options JournalOptions, consume func(context.Context, JournalEntry) error) error {
	if consume == nil {
		return errors.New("journal consumer is required")
	}
	if options.BackfillWindow == 0 {
		options.BackfillWindow = 15 * time.Minute
	}
	if options.MaxBackfill == 0 {
		options.MaxBackfill = 10_000
	}
	if options.RestartMin == 0 {
		options.RestartMin = time.Second
	}
	if options.RestartMax == 0 {
		options.RestartMax = time.Minute
	}
	if options.BackfillWindow < 0 || options.BackfillWindow > 24*time.Hour || options.MaxBackfill < 1 || options.MaxBackfill > 100_000 || options.RestartMin < 0 || options.RestartMax < options.RestartMin || options.RestartMax > 5*time.Minute {
		return errors.New("invalid journal recovery bounds")
	}
	emit := func(state, reason string, since time.Time, count uint64) error {
		status := JournalStatus{State: state, Reason: reason, At: time.Now().UTC(), Since: since, Count: count}
		var err error
		if status.QualityDegraded() && options.OnDegradation != nil {
			err = options.OnDegradation(ctx, status)
		}
		if options.OnStatus != nil {
			options.OnStatus(status)
		}
		if err != nil {
			return journalAttemptError{reason: "persist_failed"}
		}
		return nil
	}
	cursor, acknowledgedAt := options.InitialCursor, options.InitialObservedAt
	if cursor != "" && !validJournalCursor(cursor) {
		emit("gap", "invalid_cursor", acknowledgedAt, 1)
		cursor = ""
	}
	backoff := options.RestartMin
	var forceSince time.Time
	// Record quality recovery belongs to the ingest loop, not one subprocess.
	// Restarting a child cannot acknowledge a previously observed bad record.
	recoveryPending := options.InitialRecoveryPending
	for ctx.Err() == nil {
		started := time.Now().UTC()
		cutoff := started.Add(-options.BackfillWindow)
		if cursor != "" && !acknowledgedAt.IsZero() && acknowledgedAt.Before(cutoff) {
			emit("gap", "backfill_time_limit", acknowledgedAt, 1)
			cursor = ""
		}
		since := cutoff
		// Once a cursor is unavailable, its exact ordering cannot be recovered.
		// Never replay <= the acknowledged timestamp: a bounded cursor cache
		// cannot prove deduplication for an arbitrarily busy timestamp. The gap
		// includes unacknowledged entries sharing that microsecond.
		if recent := acknowledgedAt.Truncate(time.Microsecond).Add(time.Microsecond); cursor == "" && !acknowledgedAt.IsZero() && recent.After(since) {
			since = recent
		}
		if forceSince.After(since) {
			since = forceSince
		}
		if cursor == "" && acknowledgedAt.IsZero() && forceSince.IsZero() {
			emit("gap", "backfill_time_limit", cutoff, 1)
		}
		emit("starting", "process_started", started, 0)
		before := cursor
		err := j.runReliableAttempt(ctx, cursor, since, started, options.MaxBackfill, &recoveryPending, func(entry JournalEntry) error {
			ack := consume(ctx, entry)
			if ack != nil && !errors.Is(ack, ErrJournalAlreadyAcknowledged) {
				return journalAttemptError{reason: "persist_failed"}
			}
			cursor = entry.Cursor
			if entry.ReceivedAt.After(acknowledgedAt) {
				acknowledgedAt = entry.ReceivedAt
			}
			forceSince = time.Time{}
			return ack
		}, emit)
		if ctx.Err() != nil {
			return nil
		}
		reason := "process_exited"
		var failure journalAttemptError
		if errors.As(err, &failure) {
			reason = failure.reason
		}
		switch reason {
		case "cursor_unavailable", "backfill_time_limit":
			emit("gap", reason, acknowledgedAt, 1)
			cursor = ""
		case "backfill_count_limit":
			emit("gap", reason, acknowledgedAt, 1)
			// Continue at the previous launch time, so entries arriving during
			// restart are included. This explicit gap prevents unbounded replay.
			cursor = ""
			forceSince = started
		}
		emit("retrying", reason, started, 1)
		if cursor != before || time.Since(started) >= time.Minute {
			backoff = options.RestartMin
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff = min(backoff*2, options.RestartMax)
	}
	return nil
}

func validJournalCursor(cursor string) bool {
	return len(cursor) > 0 && len(cursor) <= 4096 && utf8.ValidString(cursor) && strings.IndexFunc(cursor, func(r rune) bool { return r < 0x21 || r == 0x7f }) < 0
}

func (j Journal) runReliableAttempt(ctx context.Context, cursor string, since, started time.Time, maxBackfill int, recoveryPending *bool, consume func(JournalEntry) error, emit func(string, string, time.Time, uint64) error) error {
	args := append([]string{"--follow", "--no-tail", "--system", "--boot=all", "--output=json", "--no-pager"}, journalSelectionArguments()...)
	if cursor != "" {
		// journalctl may silently approximate a vacuumed --after-cursor. Read
		// the inclusive boundary instead and verify its exact cursor before
		// consuming anything after it. Every saved cursor matched our filter.
		args = append(args, "--cursor="+cursor)
	} else {
		args = append(args, fmt.Sprintf("--since=@%d.%06d", since.Unix(), since.Nanosecond()/1000))
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(child, j.Path, args...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	command.WaitDelay = time.Second
	var stderr boundedJournalStderr
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return journalAttemptError{reason: "start_failed"}
	}
	defer stdout.Close()
	if err := command.Start(); err != nil {
		return journalAttemptError{reason: "start_failed"}
	}
	waited := false
	defer func() {
		cancel()
		if !waited {
			_ = command.Wait()
		}
	}()
	// Closing our reader also interrupts a process descendant that retained
	// the pipe; CommandContext alone only terminates the direct child.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-child.Done():
			_ = stdout.Close()
		case <-watchDone:
		}
	}()
	state := "running"
	if *recoveryPending {
		// The child is alive, but trusted ingest has not recovered. Use the
		// existing retry state without recording another historical gap.
		state = "retrying"
	}
	emit(state, "process_started", started, 0)
	reader := bufio.NewReaderSize(stdout, 64<<10)
	backfill := 0
	verifyCursor := cursor != ""
	lastCursor := cursor
	live := false
	for {
		line, oversized, readErr := readJournalLine(reader)
		if !live && (len(line) > 0 || oversized) {
			backfill++
		}
		if oversized {
			*recoveryPending = true
			if err := emit("gap", "malformed_record", started, 1); err != nil {
				return err
			}
		} else if len(line) > 0 {
			entry, ok := decodeJournalEntry(line, time.Now())
			if !ok {
				*recoveryPending = true
				if err := emit("gap", "malformed_record", started, 1); err != nil {
					return err
				}
			} else {
				if verifyCursor {
					if entry.Cursor != cursor {
						return journalAttemptError{reason: "cursor_unavailable"}
					}
					verifyCursor = false
					backfill-- // The already acknowledged boundary is not replay.
					continue
				}
				if !entry.ReceivedAt.Before(started) {
					live = true
				}
				if entry.ReceivedAt.Before(since) {
					if cursor != "" {
						return journalAttemptError{reason: "backfill_time_limit"}
					}
					if !live && backfill > maxBackfill {
						return journalAttemptError{reason: "backfill_count_limit"}
					}
					continue
				}
				if !live && backfill > maxBackfill {
					return journalAttemptError{reason: "backfill_count_limit"}
				}
				if entry.SkipReason != "" && entry.SkipReason != "unrecognized_message" {
					*recoveryPending = true
					if err := emit("degraded", entry.SkipReason, entry.ReceivedAt, 1); err != nil {
						return err
					}
				}
				// A repeated acknowledged cursor is not a new durable consumer
				// acknowledgement and cannot clear record-quality recovery.
				if entry.Cursor == lastCursor {
					continue
				}
				ack := consume(entry)
				if ack != nil && !errors.Is(ack, ErrJournalAlreadyAcknowledged) {
					return ack
				}
				lastCursor = entry.Cursor
				if ack == nil && *recoveryPending && (entry.SkipReason == "" || entry.SkipReason == "unrecognized_message") {
					// A historical gap remains in the consumer's coverage log;
					// successful trusted persistence restores current readiness.
					*recoveryPending = false
					emit("running", "record_persisted", entry.ReceivedAt, 0)
				}
			}
		}
		if !live && backfill > maxBackfill {
			return journalAttemptError{reason: "backfill_count_limit"}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return journalAttemptError{reason: "read_failed"}
			}
			break
		}
	}
	err = command.Wait()
	waited = true
	if err != nil && cursor != "" && stderr.cursorUnavailable() {
		return journalAttemptError{reason: "cursor_unavailable"}
	}
	return journalAttemptError{reason: "process_exited"}
}

func decodeJournalEntry(raw []byte, now time.Time) (JournalEntry, bool) {
	// Decode addressing metadata separately so a binary/array MESSAGE cannot
	// hide a valid journal cursor. No sender-provided field is an authority for
	// checkpoint advancement: these two fields are generated by journalctl.
	var address struct {
		Cursor   string `json:"__CURSOR"`
		Received string `json:"__REALTIME_TIMESTAMP"`
	}
	if json.Unmarshal(raw, &address) != nil || !validJournalCursor(address.Cursor) {
		return JournalEntry{}, false
	}
	microseconds, err := strconv.ParseInt(address.Received, 10, 64)
	if err != nil || microseconds <= 0 {
		return JournalEntry{}, false
	}
	entry := JournalEntry{Cursor: address.Cursor, ReceivedAt: time.UnixMicro(microseconds).UTC()}
	entry.ObservedAt = entry.ReceivedAt
	var record journalRecord
	if json.Unmarshal(raw, &record) != nil {
		entry.SkipReason = "malformed_record"
		return entry, true
	}
	if !record.trustedSSHOrigin() {
		entry.SkipReason = "untrusted_origin"
		return entry, true
	}
	entry.ObservedAt = record.observedAt(now)
	observation, ok := ParseSSH(record.Message, entry.ObservedAt)
	if !ok {
		entry.SkipReason = "unrecognized_message"
		return entry, true
	}
	entry.Observation = &observation
	return entry, true
}

// Oversized lines are drained with fixed memory instead of restarting forever
// on the same poison record. The resulting coverage gap is reported explicitly.
func readJournalLine(reader *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	oversized := false
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > 256<<10 {
			oversized = true
			line = nil
		}
		if !oversized {
			line = append(line, part...)
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, oversized, err
		}
	}
}

type boundedJournalStderr struct{ data []byte }

func (b *boundedJournalStderr) Write(data []byte) (int, error) {
	if left := 8192 - len(b.data); left > 0 {
		b.data = append(b.data, data[:min(len(data), left)]...)
	}
	return len(data), nil
}

func (b *boundedJournalStderr) cursorUnavailable() bool {
	message := strings.ToLower(string(b.data))
	return strings.Contains(message, "failed to seek to cursor") || strings.Contains(message, "cannot seek to cursor") || strings.Contains(message, "cursor not found")
}

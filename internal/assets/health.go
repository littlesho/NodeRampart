// SPDX-License-Identifier: MIT

package assets

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Health is the complete daemon-readable contract for managed GeoIP updates.
// Private management state, database paths and account credentials are absent.
type Health struct {
	SchemaVersion       int             `json:"schema_version"`
	CheckedAt           time.Time       `json:"checked_at_utc,omitzero"`
	LastSuccessAt       time.Time       `json:"last_success_at_utc,omitzero"`
	ConsecutiveFailures uint32          `json:"consecutive_failures"`
	Result              string          `json:"result"`
	Scheduled           bool            `json:"scheduled"`
	Schedule            *ScheduleHealth `json:"schedule,omitempty"`
}

// ScheduleHealth records timer configuration attempts independently from
// downloaded database verification. Scheduled changes only after success.
type ScheduleHealth struct {
	CheckedAt           time.Time `json:"checked_at_utc,omitzero"`
	ConsecutiveFailures uint32    `json:"consecutive_failures"`
	Result              string    `json:"result"`
}

// NormalizeLegacy conservatively separates an old combined schedule failure.
// Only the most recent scheduling failure is known: the old counter may also
// contain download failures. This conversion never writes metadata to disk.
func (h Health) NormalizeLegacy() Health {
	if h.Result == "schedule_failed" && h.Schedule == nil {
		checked := h.CheckedAt
		if checked.Before(h.LastSuccessAt) {
			checked = h.LastSuccessAt
		}
		h.Schedule = &ScheduleHealth{CheckedAt: checked, ConsecutiveFailures: 1, Result: "failed"}
		h.Result, h.ConsecutiveFailures, h.CheckedAt = "unknown", 0, h.LastSuccessAt
	}
	return h
}

func (h Health) Validate() error {
	if h.SchemaVersion != 1 {
		return errors.New("unsupported asset health schema")
	}
	switch h.Result {
	case "ok", "unchanged", "download_failed", "activation_failed", "schedule_failed", "credentials_unavailable", "unknown":
	default:
		return errors.New("invalid asset health result")
	}
	for _, at := range []time.Time{h.CheckedAt, h.LastSuccessAt} {
		if !at.IsZero() && (at.Year() < 1970 || at.Year() > 9999) {
			return errors.New("invalid asset health timestamp")
		}
	}
	if s := h.Schedule; s != nil {
		if h.Result == "schedule_failed" || !s.CheckedAt.IsZero() && (s.CheckedAt.Year() < 1970 || s.CheckedAt.Year() > 9999) {
			return errors.New("invalid asset schedule metadata")
		}
		switch s.Result {
		case "ok":
			if s.CheckedAt.IsZero() || s.ConsecutiveFailures != 0 {
				return errors.New("invalid asset schedule success")
			}
		case "failed":
			if s.CheckedAt.IsZero() || s.ConsecutiveFailures == 0 {
				return errors.New("invalid asset schedule failure")
			}
		case "unknown":
			if s.ConsecutiveFailures != 0 {
				return errors.New("invalid unknown asset schedule")
			}
		default:
			return errors.New("invalid asset schedule result")
		}
	}
	if !h.LastSuccessAt.IsZero() && (h.CheckedAt.IsZero() || h.LastSuccessAt.After(h.CheckedAt)) {
		return errors.New("asset success cannot follow its latest check")
	}
	if (h.Result == "ok" || h.Result == "unchanged") && (h.CheckedAt.IsZero() || h.LastSuccessAt.IsZero() || h.ConsecutiveFailures != 0) {
		return errors.New("asset success requires a verified successful check")
	}
	return nil
}

// ReadHealth opens only bounded root-owned sanitized metadata. The observer
// never reads geoip-state.json or the MaxMind credentials to determine health.
func ReadHealth(path string) (Health, error) {
	return readHealth(path, 0)
}

func readHealth(path string, owner uint32) (Health, error) {
	var value Health
	invalid := errors.New("asset health metadata is unsafe or invalid")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || len(path) > 4096 || strings.IndexFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return value, invalid
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return value, invalid
	}
	parent := os.NewFile(uintptr(fd), "asset-health-parent")
	defer func() { _ = parent.Close() }()
	parts := strings.Split(strings.TrimPrefix(filepath.Dir(path), "/"), "/")
	for i, part := range parts {
		if part != "" {
			next, err := unix.Openat(int(parent.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				if errors.Is(err, unix.ENOENT) {
					return value, os.ErrNotExist
				}
				return value, invalid
			}
			_ = parent.Close()
			parent = os.NewFile(uintptr(next), "asset-health-parent")
		}
		var st unix.Stat_t
		if unix.Fstat(int(parent.Fd()), &st) != nil || (st.Uid != 0 && st.Uid != owner) ||
			(st.Mode&0o022 != 0 && (i == len(parts)-1 || st.Mode&unix.S_ISVTX == 0)) {
			return value, invalid
		}
	}
	fd, err = unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return value, os.ErrNotExist
		}
		return value, invalid
	}
	file := os.NewFile(uintptr(fd), "asset-health")
	defer file.Close()
	var before, after unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Mode&0o777 != 0o640 || before.Nlink != 1 || before.Uid != owner || before.Size < 2 || before.Size > 4096 {
		return value, invalid
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 || unix.Fstat(fd, &after) != nil ||
		before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Uid != after.Uid || before.Gid != after.Gid ||
		before.Size != after.Size || before.Nlink != after.Nlink || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return value, invalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return Health{}, invalid
	}
	value = value.NormalizeLegacy()
	if value.Validate() != nil {
		return Health{}, invalid
	}
	return value, nil
}

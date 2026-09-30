// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/replay"
	"golang.org/x/sys/unix"
)

type ThresholdPreview struct {
	CurrentConfigSHA256 string            `json:"current_config_sha256"`
	DraftConfigSHA256   string            `json:"draft_config_sha256"`
	Comparison          replay.Comparison `json:"comparison"`
	Limitations         []string          `json:"limitations"`
}

func PreviewThresholdFiles(ctx context.Context, input, currentPath, draftPath string) (ThresholdPreview, error) {
	currentData, err := readFile(currentPath, maxManagedJSON, false, -1)
	if err != nil {
		return ThresholdPreview{}, errors.New("current configuration is unreadable or unsafe")
	}
	current, err := decodeConfig(currentData)
	if err != nil {
		return ThresholdPreview{}, errors.New("current configuration is invalid")
	}
	draftData, err := readFile(draftPath, maxManagedJSON, false, -1)
	if err != nil {
		return ThresholdPreview{}, errors.New("draft configuration is unreadable or unsafe")
	}
	draft, err := decodeConfig(draftData)
	if err != nil {
		return ThresholdPreview{}, errors.New("draft configuration is invalid")
	}
	return PreviewThresholds(ctx, input, current, draft)
}

func PreviewThresholds(ctx context.Context, input string, current, draft config.Config) (ThresholdPreview, error) {
	if err := ctx.Err(); err != nil {
		return ThresholdPreview{}, err
	}
	a, err := replay.RulesFromConfig(current)
	if err != nil {
		return ThresholdPreview{}, err
	}
	b, err := replay.RulesFromConfig(draft)
	if err != nil {
		return ThresholdPreview{}, err
	}
	comparison, err := replay.Compare(ctx, input, a, b)
	if err != nil {
		if ctx.Err() != nil {
			return ThresholdPreview{}, ctx.Err()
		}
		return ThresholdPreview{}, err
	}
	return ThresholdPreview{CurrentConfigSHA256: config.Fingerprint(current), DraftConfigSHA256: config.Fingerprint(draft), Comparison: comparison, Limitations: []string{
		"Only network and SSH detector rules are compared; other draft settings are not exercised.",
		"Fewer alerts do not establish a lower false-positive rate. Review the retained evidence and dataset limitations before saving.",
		"Preview does not save configuration. The installed configuration menu uses its existing review, confirmation and safe apply path.",
	}}, nil
}

const maxFeedbackRecords = 1000
const maxFeedbackBytes = 512 << 10
const feedbackRetention = 90 * 24 * time.Hour

var feedbackEventID = regexp.MustCompile(`^evt_[1-9][0-9]{0,8}$`)

type ThresholdFeedback struct {
	InputSHA256 string    `json:"input_sha256"`
	RulesSHA256 string    `json:"rules_sha256"`
	Side        string    `json:"side"`
	EventID     string    `json:"event_id"`
	Label       string    `json:"label"`
	RecordedAt  time.Time `json:"recorded_at_utc"`
}

type feedbackFile struct {
	Version int                 `json:"version"`
	Records []ThresholdFeedback `json:"records"`
}

type FeedbackResult struct {
	Retained      int  `json:"retained"`
	Evicted       int  `json:"evicted"`
	Replaced      bool `json:"replaced"`
	RetentionDays int  `json:"retention_days"`
	MaxRecords    int  `json:"max_records"`
}

func validFeedbackLabel(label string) bool {
	return label == "reasonable" || label == "false_positive" || label == "uncertain"
}

// RecordThresholdFeedback stores only local comparison identities and a label,
// never addresses, event bodies, free text, configuration or credentials.
func RecordThresholdFeedback(ctx context.Context, comparisonPath, path, side, eventID, label string) (FeedbackResult, error) {
	return recordThresholdFeedback(ctx, comparisonPath, path, side, eventID, label, time.Now().UTC())
}

func recordThresholdFeedback(ctx context.Context, comparisonPath, path, side, eventID, label string, now time.Time) (FeedbackResult, error) {
	result := FeedbackResult{RetentionDays: 90, MaxRecords: maxFeedbackRecords}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !validFeedbackLabel(label) || !feedbackEventID.MatchString(eventID) || side != "baseline" && side != "candidate" {
		return result, errors.New("feedback requires a retained event ID, baseline/candidate and reasonable/false_positive/uncertain")
	}
	comparison, err := replay.LoadComparison(comparisonPath)
	if err != nil {
		return result, err
	}
	selected := comparison.Candidate
	if side == "baseline" {
		selected = comparison.Baseline
	}
	found := false
	for _, event := range selected.Events {
		if event.ID == eventID {
			found = true
			break
		}
	}
	if !found {
		return result, errors.New("event is absent from the retained comparison; truncated or invented IDs cannot be marked")
	}
	if !cleanPath(path) {
		return result, errors.New("feedback requires an absolute protected local file path")
	}
	parent, err := openDirectory(filepath.Dir(path), true)
	if err != nil {
		return result, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(path)+".lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return result, errors.New("feedback lock is unavailable")
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Mode&0o077 != 0 || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return result, errors.New("feedback lock is unsafe or another update is running")
	}
	previous, err := readFile(path, maxFeedbackBytes, true, -1)
	existed := !errors.Is(err, os.ErrNotExist)
	if err != nil && existed {
		return result, err
	}
	state := feedbackFile{Version: 1, Records: []ThresholdFeedback{}}
	if existed {
		decoder := json.NewDecoder(bytes.NewReader(previous))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || state.Version != 1 || len(state.Records) > maxFeedbackRecords {
			return result, errors.New("feedback file is invalid, too large or from a future format")
		}
	}
	mark := ThresholdFeedback{InputSHA256: comparison.InputSHA256, RulesSHA256: replay.RulesFingerprint(selected.Rules), Side: side, EventID: eventID, Label: label, RecordedAt: now.UTC()}
	kept := make([]ThresholdFeedback, 0, len(state.Records)+1)
	for _, old := range state.Records {
		if !validFeedbackLabel(old.Label) || !feedbackEventID.MatchString(old.EventID) || old.Side != "baseline" && old.Side != "candidate" || !validSHA(old.InputSHA256) || !validSHA(old.RulesSHA256) || old.RecordedAt.IsZero() || old.RecordedAt.After(now.Add(5*time.Minute)) {
			return result, errors.New("feedback file contains invalid records")
		}
		if old.RecordedAt.Before(now.Add(-feedbackRetention)) {
			result.Evicted++
			continue
		}
		if old.InputSHA256 == mark.InputSHA256 && old.RulesSHA256 == mark.RulesSHA256 && old.Side == mark.Side && old.EventID == mark.EventID {
			result.Replaced = true
			continue
		}
		kept = append(kept, old)
	}
	kept = append(kept, mark)
	if len(kept) > maxFeedbackRecords {
		result.Evicted += len(kept) - maxFeedbackRecords
		kept = kept[len(kept)-maxFeedbackRecords:]
	}
	state.Records = kept
	data, _ := json.MarshalIndent(state, "", "  ")
	if len(data) > maxFeedbackBytes {
		return result, errors.New("feedback exceeds its byte budget")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	current, err := readFile(path, maxFeedbackBytes, true, -1)
	if existed && (err != nil || !bytes.Equal(previous, current)) || !existed && !errors.Is(err, os.ErrNotExist) {
		return result, errors.New("feedback changed externally before publication")
	}
	if err := writeFile(path, bytes.NewReader(data), maxFeedbackBytes, 0o600, os.Geteuid(), os.Getegid(), existed); err != nil {
		return result, err
	}
	result.Retained = len(kept)
	return result, nil
}

func validSHA(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

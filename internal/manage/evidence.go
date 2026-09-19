// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/evidence"
)

func (m *Manager) exportEvidence(ctx context.Context, input map[string]string) (string, error) {
	if m.Request == nil {
		return "", errors.New("daemon connection is unavailable")
	}
	now := time.Now().UTC()
	duration := 24 * time.Hour
	if input["incident"] != "" {
		duration = 7 * 24 * time.Hour
	}
	end, err := queryTime(input, "until", now)
	if err != nil {
		return "", err
	}
	start, err := queryTime(input, "since", end.Add(-duration))
	if err != nil {
		return "", err
	}
	query := api.EvidenceArgs{Start: start, End: end, IncidentID: input["incident"]}
	format := input["format"]
	if format == "" {
		format = "zip"
	}
	if query.Validate() != nil || input["output"] == "" || (format != "zip" && format != "html" && format != "json") {
		return "", errors.New("choose a valid period, zip/html/json format and a new file in a private directory")
	}
	data, err := m.Request(ctx, "evidence_snapshot", query)
	if err != nil {
		return "", errors.New("authenticated evidence snapshot is unavailable")
	}
	if len(data) > evidence.MaxJSONBytes {
		return "", errors.New("evidence snapshot exceeds its size limit")
	}
	var bundle evidence.Bundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&bundle) != nil || decoder.Decode(new(any)) != io.EOF {
		return "", errors.New("evidence snapshot is invalid")
	}
	if err := evidence.Export(ctx, bundle, input["output"], format); err != nil {
		return "", err
	}
	return "Local redacted evidence saved. UTC timing and counts can remain linkable; review before sharing.\n脱敏证据已保存在本地；UTC 时间和数量仍可能被关联，请检查后再分享。", nil
}

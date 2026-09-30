// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestReportExportRetainsFullChineseBodyAndEscapesHTML(t *testing.T) {
	body := strings.Repeat("完整报告与未知覆盖\n", 1500) + `<script>alert("synthetic")</script><img src="https://external.invalid/x">`
	document, err := json.Marshal(map[string]any{"title": "<unsafe-title>", "body": body, "notes": []string{"Missing is not zero."}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := store.ReportSnapshot{Date: "2026-09-29", Title: "Short preview", Body: "short preview", Document: document}
	var output bytes.Buffer
	if err := writeResponse(&output, commandOptions{Format: "html"}, snapshot); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	if strings.Contains(html, "<script>") || strings.Contains(html, "<img ") || strings.Contains(html, "<unsafe-title>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "Content-Security-Policy") || strings.Count(html, "完整报告与未知覆盖") != 1500 || !strings.Contains(html, "Missing is not zero.") {
		t.Fatal("full content truncated or active HTML escaped incorrectly")
	}
	output.Reset()
	if err := writeResponse(&output, commandOptions{Format: "json"}, snapshot); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Document struct {
			Body string `json:"body"`
		}
		State string `json:"full_content_state"`
	}
	if json.Unmarshal(output.Bytes(), &decoded) != nil || decoded.Document.Body != body || decoded.State != "original_full_snapshot" {
		t.Fatal("JSON did not retain original full document")
	}
	output.Reset()
	snapshot.Document = nil
	if err := writeResponse(&output, commandOptions{Format: "html"}, snapshot); err != nil || !strings.Contains(output.String(), "Full original content is unavailable") {
		t.Fatal("legacy short snapshot presented as reconstructed original", err)
	}
}

func TestNewReportAndNotificationCLIBoundaries(t *testing.T) {
	now := time.Now().UTC()
	for _, days := range []string{"7", "30"} {
		options, err := parseCommand([]string{"--days", days}, "report_trend", now)
		var args api.ReportTrendArgs
		if err != nil || json.Unmarshal(options.Request.Args, &args) != nil || args.Days != 7 && args.Days != 30 {
			t.Fatal("trend entry unavailable", err)
		}
	}
	for _, args := range [][]string{{"--days", "0"}, {"--days", "31"}, {"--days", "14"}} {
		if _, err := parseCommand(args, "report_trend", now); err == nil {
			t.Fatal("unsupported trend range accepted")
		}
	}
	for _, format := range []string{"json", "html"} {
		if _, err := parseCommand([]string{"--date", "2026-09-29", "--format", format}, "report_export", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := parseCommand([]string{"--date", "2026-09-29", "--format", "text"}, "report_export", now); err == nil {
		t.Fatal("unsupported export format accepted")
	}
	for _, name := range []string{"notify_test", "notify_discard_isolated"} {
		options, err := parseCommand([]string{"--channel", "webhook"}, name, now)
		var args api.NotifyChannelArgs
		if err != nil || json.Unmarshal(options.Request.Args, &args) != nil || args.Channel != "webhook" {
			t.Fatal("selected webhook entry unavailable", err)
		}
		if _, err := parseCommand([]string{"--channel", "arbitrary"}, name, now); err == nil {
			t.Fatal("arbitrary channel accepted")
		}
	}
}

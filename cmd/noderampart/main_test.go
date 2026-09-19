// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestParseCommandRejectsInvalidRequestsBeforeConfigRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"events_list", []string{"--limit", "101"}},
		{"events_list", []string{"--since", "invalid"}},
		{"events_list", []string{"--kind", "<script>"}},
		{"events_show", nil},
		{"events_show", []string{"--id", "../../private"}},
		{"report_show", []string{"--date", "2026-02-30"}},
		{"report_show", []string{"--date", "2026-09-11", "--format", "executable"}},
		{"report_list", []string{"--before", "invalid"}},
		{"notify_retry", []string{"--id", "msg_1", "extra"}},
		{"notify_quarantine", nil},
		{"notify_resume", []string{"--destination", "invalid/value"}},
		{"status", []string{"--id", "unrelated"}},
		{"backup_create", []string{"--output", "relative"}},
	} {
		t.Run(tc.name+strings.Join(tc.args, "_"), func(t *testing.T) {
			if _, err := parseCommand(tc.args, tc.name, time.Now().UTC()); err == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
}

func TestEventPaginationCommandPreservesBoundary(t *testing.T) {
	stamp := "2026-09-11T03:04:05.123Z"
	options, err := parseCommand([]string{"--since", "2026-09-01T00:00:00Z", "--until", stamp, "--before-id", "evt_previous", "--limit", "100", "--manual-current-uid"}, "events_list", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var args api.EventListArgs
	if json.Unmarshal(options.Request.Args, &args) != nil || args.End.Format(time.RFC3339Nano) != stamp || args.BeforeID != "evt_previous" || args.Limit != 100 || !options.Manual {
		t.Fatal("pagination boundary was lost")
	}
}

func TestReportExportIsStandaloneAndEscapesStoredText(t *testing.T) {
	var output bytes.Buffer
	data := map[string]string{"title": "<script>title</script>", "body": "<b>Summary</b>\n&lt;script&gt;synthetic&lt;/script&gt;\n&#27;[31m\u009b31m"}
	if err := writeResponse(&output, commandOptions{Format: "html"}, data); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.HasPrefix(text, "<!doctype html>") || strings.Contains(text, "<script>") || !strings.Contains(text, "&lt;script&gt;") || strings.ContainsRune(text, '\x1b') {
		t.Fatal("HTML export is unsafe or incomplete")
	}
	output.Reset()
	if err := writeResponse(&output, commandOptions{Format: "text"}, data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "<b>") || strings.ContainsRune(output.String(), '\x1b') || strings.ContainsRune(output.String(), '\u009b') {
		t.Fatal("plain export contains formatting or terminal controls")
	}
}

func TestDoctorWorksWithMissingAndBrokenConfigWithoutLeakingValues(t *testing.T) {
	for _, exists := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "config.json")
		if exists {
			if err := os.WriteFile(path, []byte(`{"synthetic_private_value":"never-print"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var output bytes.Buffer
		if err := doctorCommand([]string{"--config", path}, &output); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), "synthetic_private") || strings.Contains(output.String(), "never-print") || !strings.Contains(output.String(), `"configuration"`) {
			t.Fatal("doctor leaked invalid config or omitted diagnostics")
		}
	}
}

func TestDoctorDetectsSourcePackageUnitConflict(t *testing.T) {
	root := t.TempDir()
	for _, item := range []struct{ path, body string }{
		{"etc/systemd/system/noderampartd.service", "[Service]\nExecStart=/usr/local/bin/noderampartd\nEnvironment=SYNTHETIC_PRIVATE=never-print\n"},
		{"usr/lib/systemd/system/noderampartd.service", "[Service]\nExecStart=/usr/bin/noderampartd\n"},
	} {
		path := filepath.Join(root, item.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(item.body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	checks := inspectUnits(root)
	if len(checks) != 2 || checks[0].State != "conflict" {
		t.Fatal("source unit overriding package unit was not detected")
	}
	data, _ := json.Marshal(checks)
	if strings.Contains(string(data), "SYNTHETIC_PRIVATE") {
		t.Fatal("unit content leaked into diagnostics")
	}
}

func TestRestoreRequiresAbsentControlSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	if err := requireDaemonStopped(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireDaemonStopped(path); err == nil {
		t.Fatal("existing control socket path must block restore")
	}
}

func TestControlJSONExportPreservesLargeCounters(t *testing.T) {
	var frame bytes.Buffer
	if err := protocol.WriteFrame(&frame, api.Response{Version: api.Version, OK: true, Data: map[string]uint64{"bytes": 9007199254740993}}); err != nil {
		t.Fatal(err)
	}
	response, err := readResponse(bufio.NewReader(&frame))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := writeResponse(&output, commandOptions{Format: "json"}, response.Data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "9007199254740993") {
		t.Fatal("large counter lost integer precision")
	}
}

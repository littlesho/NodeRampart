// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
)

func controlTestApp(t *testing.T) *App {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &App{options: Options{Store: db, Config: config.Defaults()}}
}

func controlRequest(t *testing.T, app *App, command string, args any) api.Response {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return app.controlResponse(context.Background(), api.Request{Version: api.Version, Command: command, Args: data})
}

func TestControlRejectsMalformedArguments(t *testing.T) {
	app := controlTestApp(t)
	for _, tc := range []struct{ command, args string }{
		{"notify_status", `{"synthetic_private":"never-print"}`},
		{"notify_list", `{"limit":101}`},
		{"notify_retry", `{"id":"../private"}`},
		{"notify_quarantine", `{}`},
		{"events_list", `{"limit":20}`},
		{"events_show", `{"id":"evt_1"} {}`},
		{"report_show", `{"date":"2026-02-30"}`},
		{"report_list", `{"limit":0}`},
		{"notify_resume", `null`},
		{"backup_create", `{"output":"/tmp/outside-state.db"}`},
	} {
		t.Run(tc.command, func(t *testing.T) {
			response := app.controlResponse(context.Background(), api.Request{Version: api.Version, Command: tc.command, Args: json.RawMessage(tc.args)})
			if response.OK || strings.Contains(response.Error, "synthetic_private") {
				t.Fatal("malformed arguments were accepted or exposed")
			}
		})
	}
}

func TestControlEventListUsesBoundedSummariesAndPagination(t *testing.T) {
	app := controlTestApp(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 21; i++ {
		event := model.Event{ID: fmt.Sprintf("evt_%03d", i), ObservedAt: now, Kind: "ssh_login_success", Severity: model.SeverityMedium, Summary: "synthetic summary", Evidence: map[string]string{"synthetic_private_evidence": strings.Repeat("x", 60<<10)}}
		if err := app.options.Store.InsertEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	response := controlRequest(t, app, "events_list", api.EventListArgs{Start: now.Add(-time.Hour), End: now.Add(time.Hour), Limit: 20})
	encoded, err := json.Marshal(response)
	if err != nil || !response.OK || len(encoded) > protocol.MaxFrameSize || strings.Contains(string(encoded), "synthetic_private_evidence") {
		t.Fatal("event list must contain bounded summaries without evidence bodies")
	}
	var page struct {
		Data struct {
			Events []eventListItem `json:"events"`
			Before string          `json:"next_before_id"`
			Until  time.Time       `json:"next_until_utc"`
		} `json:"data"`
	}
	if err := json.Unmarshal(encoded, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data.Events) != 20 || page.Data.Before == "" || !page.Data.Until.Equal(now) {
		t.Fatal("event page lacks exact timestamp and ID cursor")
	}
	response = controlRequest(t, app, "events_list", api.EventListArgs{Start: now.Add(-time.Hour), End: page.Data.Until, BeforeID: page.Data.Before, Limit: 20})
	encoded, _ = json.Marshal(response)
	page.Data.Events = nil
	if json.Unmarshal(encoded, &page) != nil || len(page.Data.Events) != 1 || page.Data.Events[0].ID != "evt_000" {
		t.Fatal("next page duplicated or skipped same-time events")
	}
}

func TestControlNotificationTransitionsAreExplicitAndBodyFree(t *testing.T) {
	app := controlTestApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := app.options.Store.Enqueue(ctx, store.OutboxMessage{ID: "msg_1", DedupeKey: "test", Destination: "telegram", Body: "synthetic_private_body", NextAttempt: now}); err != nil {
		t.Fatal(err)
	}
	if response := controlRequest(t, app, "notify_quarantine", api.IDArgs{ID: "msg_missing"}); response.OK {
		t.Fatal("missing notification reported as quarantined")
	}
	if response := controlRequest(t, app, "notify_quarantine", api.IDArgs{ID: "msg_1"}); !response.OK {
		t.Fatal(response.Error)
	}
	response := controlRequest(t, app, "notify_list", api.ListArgs{Limit: 20})
	encoded, _ := json.Marshal(response)
	if !response.OK || strings.Contains(string(encoded), "synthetic_private_body") || !strings.Contains(string(encoded), "quarantined") {
		t.Fatal("notification list leaked body or lost state")
	}
	if response := controlRequest(t, app, "notify_retry", api.IDArgs{ID: "msg_1"}); !response.OK {
		t.Fatal(response.Error)
	}
	if err := app.options.Store.MarkRateLimited(ctx, "msg_1", "telegram", time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), "synthetic hold"); err != nil {
		t.Fatal(err)
	}
	if response := controlRequest(t, app, "notify_resume", api.DestinationArgs{Destination: "telegram"}); !response.OK {
		t.Fatal(response.Error)
	}
	pending, err := app.options.Store.Pending(ctx, time.Now().UTC().Add(time.Second), 20)
	if err != nil || len(pending) != 1 {
		t.Fatal("explicit destination resume did not make held message eligible")
	}
}

func TestControlReportArchiveAndErrorsAreSafe(t *testing.T) {
	app := controlTestApp(t)
	now := time.Now().UTC()
	if err := app.options.Store.SaveReport(context.Background(), store.ReportSnapshot{Date: "2026-09-10", Title: "Synthetic", Body: "synthetic archived body", PeriodStart: now.Add(-24 * time.Hour), PeriodEnd: now, GeneratedAt: now}); err != nil {
		t.Fatal(err)
	}
	response := controlRequest(t, app, "report_show", api.DateArgs{Date: "2026-09-10"})
	encoded, _ := json.Marshal(response)
	if !response.OK || !strings.Contains(string(encoded), "synthetic archived body") {
		t.Fatal("archived report not returned")
	}
	if err := app.options.Store.Close(); err != nil {
		t.Fatal(err)
	}
	response = controlRequest(t, app, "events_show", api.IDArgs{ID: "evt_1"})
	if response.OK || response.Error != "event unavailable" {
		t.Fatal("storage error exposed implementation details")
	}
}

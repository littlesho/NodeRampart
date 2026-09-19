// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/detect"
	"github.com/littlesho/NodeRampart/internal/model"
)

func TestControlTimelineIncidentAndSilenceLifecycle(t *testing.T) {
	app := controlTestApp(t)
	app.network = detect.NewNetwork(app.options.Config.Detection)
	now := time.Now().UTC()
	e := model.Event{ID: "evt_control", IncidentID: "inc_control", ObservedAt: now, Kind: "syn_flood", Phase: "start", Summary: "synthetic"}
	if err := app.options.Store.InsertEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	q := api.TimelineArgs{Start: now.Add(-time.Hour), End: now.Add(time.Hour), IncidentID: e.IncidentID, Limit: 20}
	for _, command := range []string{"events_timeline", "incident_show"} {
		response := controlRequest(t, app, command, q)
		if !response.OK {
			t.Fatal(command, response.Error)
		}
		data, _ := json.Marshal(response.Data)
		if !strings.Contains(string(data), e.ID) {
			t.Fatal("control omitted retained incident event")
		}
	}
	added := controlRequest(t, app, "notify_silence_add", api.SilenceArgs{IncidentID: e.IncidentID, ExpiresAt: now.Add(time.Hour)})
	if !added.OK {
		t.Fatal(added.Error)
	}
	encoded, _ := json.Marshal(added.Data)
	var result struct {
		Silence struct {
			ID string `json:"id"`
		} `json:"silence"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil || result.Silence.ID == "" {
		t.Fatal("missing silence identity", err)
	}
	if response := controlRequest(t, app, "notify_silence_remove", api.IDArgs{ID: result.Silence.ID}); !response.OK {
		t.Fatal(response.Error)
	}
	listed := controlRequest(t, app, "notify_silence_list", struct{}{})
	data, _ := json.Marshal(listed.Data)
	if !listed.OK || !strings.Contains(string(data), "revoked") {
		t.Fatal("revoked silence not inspectable")
	}
	if response := controlRequest(t, app, "health", api.HealthArgs{Start: now.Add(-time.Hour), End: now, Limit: 100}); !response.OK {
		t.Fatal(response.Error)
	}
	for _, command := range []string{"events_timeline", "incident_show", "incident_list", "notify_silence_add", "notify_silence_remove", "notify_silence_list", "health"} {
		response := controlRequest(t, app, command, map[string]string{"synthetic_private": "do not echo"})
		if response.OK || strings.Contains(response.Error, "synthetic_private") {
			t.Fatal("strict argument rejection failed", command)
		}
	}
}

// SPDX-License-Identifier: MIT

package evidence

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/littlesho/NodeRampart/internal/store"
)

func TestEvidenceNativePartialSuccessAndRekeyedDeliveryIdentities(t *testing.T) {
	raw := rawFixture()
	raw.Timeline.Events[0].Deliveries = []store.DeliveryOutcome{{Channel: "slack", Decision: "queued", State: "sent", NotificationID: "private_slack_id"}, {Channel: "wecom", Decision: "queued", State: "quarantined", NotificationID: "private_wecom_id"}, {Channel: "teams", Decision: "queued", State: "accepted", NotificationID: "private_teams_id"}}
	b, err := Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Events[0].Deliveries) != 3 || b.Events[0].Deliveries[1].State != "quarantined" || b.Events[0].Deliveries[2].State != "accepted" {
		t.Fatal("partial outcome collapsed")
	}
	data, _ := json.Marshal(b)
	if strings.Contains(string(data), "private_") {
		t.Fatal("raw identity escaped projection")
	}
	copy, err := renewAliases(b)
	if err != nil {
		t.Fatal(err)
	}
	for i := range b.Events[0].Deliveries {
		if copy.Events[0].Deliveries[i].NotificationAlias == b.Events[0].Deliveries[i].NotificationAlias {
			t.Fatal("delivery alias was reused")
		}
	}
	b.Events[0].Deliveries = append(b.Events[0].Deliveries, b.Events[0].Deliveries...)
	b.Events[0].Deliveries = append(b.Events[0].Deliveries, b.Events[0].Deliveries...)
	if b.Validate() == nil {
		t.Fatal("unbounded delivery array accepted")
	}
}

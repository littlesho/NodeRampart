// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/store"
)

type dailyOfficialFixture struct{ reject bool }

func (p dailyOfficialFixture) PrepareMessage(m *store.OutboxMessage) error {
	if p.reject {
		return errors.New("synthetic renderer refusal")
	}
	if !json.Valid([]byte(m.SemanticPayload)) || m.LogicalKind != "daily" {
		return errors.New("daily semantics not frozen")
	}
	data, _ := json.Marshal(map[string]string{"body": m.Body})
	m.FrozenPayload = string(data)
	if m.Channel == "twilio_sms" {
		m.Encoding = "gsm7"
		m.EstimatedSegments = 1
	}
	return nil
}

func TestOfficialDailyIndependentFrozenAndNoHistoryBackfill(t *testing.T) {
	b, _, now := completeUsageBuilder(t)
	ctx := context.Background()
	date, _, end := PreviousDay(now, time.UTC)
	s := Scheduler{Store: b.Store, Builder: b, DailyAt: "00:00", Hostname: "synthetic-host", NotificationPrivacy: "prefix", NativeLanguages: map[string]string{}, OfficialNotifiers: map[string]OfficialPreparer{}}
	for i, ch := range config.OfficialChannelNames() {
		dest := ch + ":" + strings.Repeat(string(rune('a'+i)), 64)
		if err := b.Store.ConfigureNotificationTarget(ctx, ch, dest, "prefix", true, end.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		policy := store.OfficialChannelPolicy{Enabled: true, ConsentedAt: end.Add(-time.Hour), Purpose: "synthetic daily", EvidenceRef: "synthetic-consent", BasisID: strings.Repeat("a", 32), NotificationTypes: []string{"daily"}, CostConfirmed: true, DailyMessageLimit: 20}
		if ch == "twilio_sms" {
			policy.MaxSegments = 2
			policy.DailySegmentLimit = 40
		}
		if err := b.Store.ConfigureOfficialPolicy(ctx, ch, policy, now); err != nil {
			t.Fatal(err)
		}
		s.Destinations = append(s.Destinations, dest)
		s.NativeLanguages[ch] = "zh"
		s.OfficialNotifiers[ch] = dailyOfficialFixture{reject: ch == "whatsapp_cloud"}
	}
	for i := 0; i < 2; i++ {
		if err := s.checkPrevious(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := b.Store.Notifications(ctx, "", 20)
	if err != nil || len(rows) != 4 {
		t.Fatal("daily per-channel dedupe", len(rows), err)
	}
	for _, row := range rows {
		if row.Channel == "whatsapp_cloud" && row.State != "blocked" {
			t.Fatal("rejected template was treated as sent or erased")
		}
	}
	for i, dest := range s.Destinations {
		generated, err := b.Store.ReportGenerated(ctx, date, dest)
		if err != nil || !generated {
			t.Fatal("daily local decision missing", err)
		}
		pending, err := b.Store.PendingDestination(ctx, time.Now().Add(time.Minute), 20, dest)
		want := 1
		if i == 3 {
			want = 0
		}
		if err != nil || len(pending) != want {
			t.Fatal("one renderer affected another channel", i, len(pending), err)
		}
		if want > 0 && (pending[0].Language != "zh" || pending[0].LogicalKind != "daily" || pending[0].FrozenPayload == "") {
			t.Fatal("old daily context missing")
		}
	}
	// A new destination activated after this period may not receive the archive.
	ch := "line"
	dest := ch + ":" + strings.Repeat("e", 64)
	if err := b.Store.ConfigureNotificationTarget(ctx, ch, dest, "prefix", true, end.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.Destinations = []string{dest}
	if err := s.checkPrevious(ctx, now); err != nil {
		t.Fatal(err)
	}
	pending, err := b.Store.PendingDestination(ctx, time.Now().Add(time.Minute), 20, dest)
	if err != nil || len(pending) != 0 {
		t.Fatal("new daily target got historical archive", err)
	}
}

func TestOfficialDailySemanticCoverageAndTimezoneRemainArchived(t *testing.T) {
	snapshot, _ := localizedReportFixture(t)
	for _, language := range []string{"en", "zh"} {
		m, err := prepareOfficialDaily(snapshot, "synthetic\n@host", language, "line:"+strings.Repeat("a", 64), "prefix", "America/New_York", dailyOfficialFixture{})
		if err != nil {
			t.Fatal(err)
		}
		if m.Language != language || m.Timezone != "America/New_York" || !strings.Contains(m.Body, "UTC-05:00") || !strings.Contains(m.Body, "UTC-04:00") || strings.Contains(m.SemanticPayload, "\\n") || strings.Contains(m.SemanticPayload, "@host") {
			t.Fatal("frozen coverage or safe semantic projection lost")
		}
		var v officialDailySemantic
		if json.Unmarshal([]byte(m.SemanticPayload), &v) != nil || v.Coverage == "" || v.Time != "2026-03-08-0500/09-0400" || !strings.Contains(v.BoundedSummary, "4096B") || !strings.Contains(v.BoundedSummary, "drop") && language == "en" {
			t.Fatal("core daily metrics missing", v)
		}
	}
}

func TestPaidDailyActualPreparationPreservesArchivedOffsetsAndUnits(t *testing.T) {
	snapshot, _ := localizedReportFixture(t)
	for _, language := range []string{"en", "zh"} {
		for _, channel := range []string{"twilio_sms", "whatsapp_cloud"} {
			t.Run(channel+"/"+language, func(t *testing.T) {
				credential := config.OfficialCredential{Channel: channel}
				if channel == "twilio_sms" {
					credential.AccountSID = "AC" + strings.Repeat("a", 32)
					credential.AuthMode, credential.APIKeySID, credential.APIKeySecret = "api_key", "SK"+strings.Repeat("b", 32), strings.Repeat("c", 32)
					credential.From, credential.To = "+12025550141", "+12025550142"
				} else {
					credential.PhoneNumberID, credential.AccessToken, credential.Recipient, credential.GraphVersion = "12345678901", strings.Repeat("d", 32), "12025550142", config.WhatsAppGraphVersion
					code := "en_US"
					if language == "zh" {
						code = "zh_CN"
					}
					credential.Templates = &config.OfficialTemplates{Daily: &config.OfficialTemplate{Name: "noderampart_daily", Language: code, Parameters: []string{"event_kind", "phase", "severity", "time", "bounded_summary", "local_reference"}}}
				}
				path := filepath.Join(t.TempDir(), "synthetic.credential.json")
				data, err := json.Marshal(credential)
				if err != nil || os.WriteFile(path, data, 0600) != nil {
					t.Fatal("synthetic credential creation")
				}
				cfg := config.DefaultOfficialChannel(channel)
				cfg.CredentialFile, cfg.Language, cfg.DailyEnabled = path, language, true
				cfg.EventsEnabled = false
				cfg.Subscription = config.OfficialSubscription{ConfirmedAt: "2026-03-01T00:00:00Z", Purpose: "Synthetic daily", NotificationTypes: []string{"daily"}, EvidenceRef: "synthetic-daily", BasisID: strings.Repeat("a", 32), CostConfirmed: true}
				sender, err := notify.NewOfficial(channel, cfg)
				if err != nil {
					t.Fatal(err)
				}
				m, err := prepareOfficialDaily(snapshot, "synthetic-host", language, sender.Destination(), "prefix", "Asia/Tokyo", sender)
				if err != nil || m.AdmissionFailure != "" || !strings.Contains(m.FrozenPayload, "2026-03-08-0500/09-0400") || !strings.Contains(m.FrozenPayload, "4096B") || !strings.Contains(m.FrozenPayload, "8192B") {
					t.Fatal("actual paid request lost archived period, units or was unreachable", err, m.AdmissionFailure)
				}
				if channel == "twilio_sms" && (m.EstimatedSegments < 1 || m.EstimatedSegments > 2 || !strings.Contains(m.Body, "STOP") || !strings.Contains(m.Body, "sudo noderampart report list")) {
					t.Fatal("SMS paid daily essential content or estimate missing")
				}
			})
		}
	}
}

func TestOfficialDailyPeriodYearMonthAndNonMidnight(t *testing.T) {
	start := time.Date(2026, 12, 31, 23, 30, 0, 0, time.FixedZone("synthetic", 19800))
	end := start.Add(time.Hour)
	if got := officialDailyPeriod(start, end); got != "2026-12-31T23:30:00+0530/2027-01-01T00:30:00+0530" {
		t.Fatal(got)
	}
	if officialDailyBytes(4096) != "4096B" || !strings.HasPrefix(officialDailyBytes(3_000_000_000), "~") || !strings.HasSuffix(officialDailyBytes(3_000_000_000), "GiB") {
		t.Fatal("rounded metrics must state byte unit and visible approximation")
	}
}

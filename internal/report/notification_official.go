// SPDX-License-Identifier: MIT

package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

type OfficialPreparer interface {
	PrepareMessage(*store.OutboxMessage) error
}
type officialDailySemantic struct {
	HostAlias      string `json:"host_alias"`
	EventKind      string `json:"event_kind"`
	Phase          string `json:"phase"`
	Severity       string `json:"severity"`
	Time           string `json:"time"`
	BoundedSummary string `json:"bounded_summary"`
	LocalReference string `json:"local_reference"`
	Coverage       string `json:"coverage"`
}

// prepareOfficialDaily retains the archived period and coverage semantics. The
// paid renderer receives only bounded scalars, never an archive or raw logs.
func prepareOfficialDaily(snapshot store.ReportSnapshot, hostname, language string, destination, privacy, timezone string, sender OfficialPreparer) (store.OutboxMessage, error) {
	body, err := NativeNotificationBody(snapshot, language)
	if err != nil {
		return store.OutboxMessage{}, err
	}
	zh := language == "zh"
	tr := func(en, cn string) string {
		if zh {
			return cn
		}
		return en
	}
	semantic := officialDailySemantic{HostAlias: officialHost(hostname), EventKind: tr("daily", "日报"), Phase: tr("summary", "摘要"), Severity: tr("INFO", "信息"), Time: snapshot.Date, BoundedSummary: tr("statistics unknown", "统计未知"), Coverage: tr("coverage unknown", "覆盖未知"), LocalReference: "sudo noderampart report list"}
	if len(snapshot.Document) > 0 {
		doc, e := DecodeDocument(snapshot.Document)
		if e != nil {
			return store.OutboxMessage{}, e
		}
		if doc.Date != snapshot.Date || !doc.PeriodStart.Equal(snapshot.PeriodStart) || !doc.PeriodEnd.Equal(snapshot.PeriodEnd) {
			return store.OutboxMessage{}, errors.New("official daily report period conflict")
		}
		location, known, e := notificationLocation(doc.Timezone)
		if e != nil {
			return store.OutboxMessage{}, e
		}
		semantic.Time = officialDailyPeriod(doc.PeriodStart.In(location), doc.PeriodEnd.In(location))
		s := doc.Summary
		semantic.BoundedSummary = fmt.Sprintf(tr("RX%s TX%s drop%d err%d/%d", "收%s 发%s 丢%d 错%d/%d"), officialDailyBytes(s.Interface.RXBytes), officialDailyBytes(s.Interface.TXBytes), s.KernelDrops, s.ParseErrors, s.KernelStatsErrors)
		semantic.Coverage = tr("coverage partial/unknown", "覆盖部分/未知")
		if notificationCoverage(doc, "interface_counter") == "complete" && notificationCoverage(doc, "sensor_feed") == "complete" && notificationCoverage(doc, "ssh_journal") == "complete" && len(doc.Integrity.Gaps) == 0 && !doc.Integrity.HistoryTruncated && !doc.Integrity.GapsTruncated && !doc.Integrity.More {
			semantic.Coverage = tr("recorded coverage; not lossless", "记录覆盖;不保证无丢失")
		}
		if !known {
			semantic.Coverage = tr("timezone/coverage unknown", "时区/覆盖未知")
		}
	}
	payload, err := json.Marshal(semantic)
	if err != nil {
		return store.OutboxMessage{}, err
	}
	channel := strings.SplitN(destination, ":", 2)[0]
	if !config.IsOfficialChannel(channel) {
		return store.OutboxMessage{}, errors.New("invalid official report target")
	}
	m := store.OutboxMessage{ID: model.NewID("msg"), DedupeKey: "daily:" + snapshot.Date + ":" + destination, Channel: channel, PrivacyMode: privacy, Destination: destination, Body: body, Language: language, Timezone: timezone, LogicalKind: "daily", SemanticPayload: string(payload)}
	p, ok := sender.(interface {
		PrepareMessage(*store.OutboxMessage) error
	})
	if !ok || p.PrepareMessage(&m) != nil {
		m.AdmissionFailure = "official_render_rejected"
	}
	return m, nil
}

// Compact archived interval, end exclusive. Identical year/month may be
// omitted from the end; each endpoint retains its own UTC offset for DST.
func officialDailyPeriod(start, end time.Time) string {
	part := func(t time.Time, date string) string {
		if t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0 {
			date += t.Format("T15:04:05")
		}
		return date + t.Format("-0700")
	}
	last := end.Format("2006-01-02")
	if end.Year() == start.Year() {
		last = end.Format("01-02")
		if end.Month() == start.Month() {
			last = end.Format("02")
		}
	}
	return part(start, start.Format("2006-01-02")) + "/" + part(end, last)
}

func officialDailyBytes(value uint64) string {
	if value < 1_000_000 {
		return fmt.Sprintf("%dB", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	n, unit := float64(value)/1024, 0
	for n >= 1024 && unit < len(units)-1 {
		n, unit = n/1024, unit+1
	}
	return fmt.Sprintf("~%.3g%s", n, units[unit])
}

func officialHost(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			r = ' '
		}
		if r == '@' {
			r = '＠'
		}
		if b.Len()+len(string(r))+3 > 80 {
			b.WriteString("...")
			break
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "host"
	}
	return b.String()
}

// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"strconv"
	"strings"
	"time"
)

const maxBrowserHistory = 128

type browser struct {
	action action
	pages  []map[string]string
	index  int
	raw    string
	back   func()
}

func browsable(id string) bool {
	switch id {
	case "report_list", "notify_list", "incident_list", "incident_show", "timeline", "health", "retention":
		return true
	}
	return false
}

// Freeze default bounds once; next-page requests never silently move "now".
func browserArguments(id string, input map[string]string, now time.Time) map[string]string {
	args := maps.Clone(input)
	if args == nil {
		args = map[string]string{}
	}
	if id != "report_list" && id != "notify_list" {
		duration := 7 * 24 * time.Hour
		if id == "health" {
			duration = 24 * time.Hour
		}
		if args["since"] == "" {
			args["since"] = now.Add(-duration).UTC().Format(time.RFC3339Nano)
		}
		if args["until"] == "" {
			args["until"] = now.UTC().Format(time.RFC3339Nano)
		}
	}
	return args
}

func (u *ui) startBrowser(a action, args map[string]string, back func()) {
	b := &browser{action: a, back: back}
	input := browserArguments(a.id, args, time.Now())
	clearArguments(args)
	u.fetchBrowser(b, input, 1)
}

func (u *ui) fetchBrowser(b *browser, args map[string]string, move int) {
	back := b.back
	if len(b.pages) > 0 {
		back = func() { u.showBrowser(b) }
	}
	u.background(u.tr(b.action.en, b.action.zh), func(ctx context.Context) func() {
		raw, err := u.backend.Action(ctx, b.action.id, maps.Clone(args))
		return func() {
			if err != nil {
				u.output(u.tr("Query did not complete", "查询未完成"), err.Error(), back)
				return
			}
			if len(raw) > maxOutputBytes {
				u.output(u.tr(b.action.en, b.action.zh), raw, back)
				return
			}
			if move > 0 {
				if len(b.pages) > 0 {
					b.pages = b.pages[:b.index+1]
				}
				b.pages = append(b.pages, maps.Clone(args))
				if len(b.pages) > maxBrowserHistory {
					b.pages = append([]map[string]string(nil), b.pages[1:]...)
				}
				b.index = len(b.pages) - 1
			} else if move < 0 {
				b.index--
			}
			b.raw = raw
			u.showBrowser(b)
		}
	}, back)
}

func decodeBrowser(raw string) (map[string]json.RawMessage, bool) {
	if len(raw) > maxOutputBytes {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	var value map[string]json.RawMessage
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value == nil {
		return nil, false
	}
	return value, true
}

func browserString(m map[string]json.RawMessage, key string) string {
	var value string
	if json.Unmarshal(m[key], &value) != nil || len(value) > 4096 {
		return ""
	}
	return value
}

func browserMore(m map[string]json.RawMessage) bool {
	var value bool
	_ = json.Unmarshal(m["more"], &value)
	return value
}

func browserNext(id string, current map[string]string, m map[string]json.RawMessage) map[string]string {
	next := maps.Clone(current)
	switch id {
	case "retention":
		var before int64
		if !browserMore(m) || json.Unmarshal(m["next_before_id"], &before) != nil || before < 1 {
			return nil
		}
		next["before_id"] = strconv.FormatInt(before, 10)
	case "report_list", "notify_list":
		v := browserString(m, "next_before")
		if v == "" || len(v) > 128 {
			return nil
		}
		next["before"] = v
	case "incident_list":
		if !browserMore(m) {
			return nil
		}
		next["before"], next["before_id"] = browserString(m, "next_before_utc"), browserString(m, "next_before_id")
		if next["before"] == "" || next["before_id"] == "" {
			return nil
		}
	case "incident_show", "timeline":
		if id == "incident_show" {
			if m = browserNested(m, "timeline"); m == nil {
				return nil
			}
		}
		if !browserMore(m) {
			return nil
		}
		next["after"], next["after_id"] = browserString(m, "next_after_utc"), browserString(m, "next_after_id")
		if next["after"] == "" || next["after_id"] == "" {
			return nil
		}
	case "health":
		if m = browserNested(m, "history"); m == nil || !browserMore(m) {
			return nil
		}
		var offset int
		if json.Unmarshal(m["next_offset"], &offset) != nil || offset < 1 || offset > 20256 {
			return nil
		}
		next["offset"] = strconv.Itoa(offset)
	default:
		return nil
	}
	if maps.Equal(current, next) {
		return nil
	}
	return next
}

func (u *ui) showBrowser(b *browser) {
	m, ok := decodeBrowser(b.raw)
	if !ok {
		u.output(u.tr(b.action.en, b.action.zh), b.raw, b.back)
		return
	}
	current := b.pages[b.index]
	items := []menuItem{}
	rowsMap := m
	key := map[string]string{"report_list": "reports", "notify_list": "notifications", "incident_list": "incidents", "timeline": "events", "incident_show": "events", "retention": "entries"}[b.action.id]
	if b.action.id == "incident_show" {
		rowsMap = browserNested(m, "timeline")
	}
	var rows []json.RawMessage
	if key != "" {
		_ = json.Unmarshal(rowsMap[key], &rows)
	}
	if len(rows) > 100 {
		u.output(u.tr("Result unavailable", "结果不可用"), u.tr("Result exceeds list bounds.", "结果超出列表限制。"), b.back)
		return
	}
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) != nil {
			continue
		}
		id, date := browserString(fields, "id"), browserString(fields, "date")
		label := id
		if b.action.id == "report_list" {
			label = date
		}
		if b.action.id == "retention" {
			var entryID int64
			if json.Unmarshal(fields["id"], &entryID) != nil || entryID < 1 {
				continue
			}
			label = strconv.FormatInt(entryID, 10) + " · " + retentionLabel("dataset", browserString(fields, "dataset"), u.lang)
		}
		detail := browserString(fields, "summary")
		if detail == "" {
			detail = browserString(fields, "title")
		}
		if detail == "" {
			detail = browserString(fields, "kind")
		}
		if detail == "" {
			detail = browserString(fields, "state")
		}
		if b.action.id == "retention" {
			detail = retentionLabel("reason", browserString(fields, "reason"), u.lang) + " · " + browserString(fields, "data_start_utc")
		}
		entry := string(row)
		items = append(items, menuItem{label, label, detail, detail, func() {
			back := func() { u.showBrowser(b) }
			switch b.action.id {
			case "report_list":
				a, _ := findAction("report_show")
				u.executeAction(a, map[string]string{"date": date}, back)
			case "incident_list":
				a, _ := findAction("incident_show")
				u.startBrowser(a, map[string]string{"id": id, "since": current["since"], "until": current["until"], "limit": current["limit"]}, back)
			default:
				u.output(label, humanResult(entry, u.lang), back)
			}
		}})
	}
	if b.action.id == "incident_show" {
		items = append(items, menuItem{"Export this incident", "导出此 Incident", "Save an offline redacted evidence file for this incident and period.", "为此 Incident 和时间段保存离线脱敏证据文件。", func() {
			u.actionForm(incidentEvidenceAction(current), func() { u.showBrowser(b) })
		}})
	}
	items = append(items, menuItem{"Full page details", "本页完整详情", "View retained records and coverage notes.", "查看保留记录及完整性说明。", func() {
		u.output(u.tr(b.action.en, b.action.zh), humanResult(b.raw, u.lang), func() { u.showBrowser(b) })
	}})
	if next := browserNext(b.action.id, current, m); next != nil {
		items = append(items, menuItem{"Next page", "下一页", "Keep the same query period.", "保持相同查询时间段。", func() { u.fetchBrowser(b, next, 1) }})
	}
	if b.index > 0 {
		items = append(items, menuItem{"Previous page", "上一页", "Reload the previous cursor; retained data may have changed.", "按上次游标重读；保留的数据可能已变化。", func() { u.fetchBrowser(b, b.pages[b.index-1], -1) }})
	}
	items = append(items, menuItem{"Change query", "修改查询", "Choose another period or page size.", "选择其他时间段或每页条数。", func() {
		a := browserQueryAction(b.action, current)
		u.actionForm(a, b.back)
	}})
	title := u.tr(b.action.en, b.action.zh)
	if len(rows) == 0 && key != "" {
		title += u.tr(" · no records", " · 无记录")
	}
	u.menu(title, title, items, b.back)
}

func incidentEvidenceAction(current map[string]string) action {
	a, _ := findAction("evidence_export")
	a.params = append([]parameter(nil), a.params...)
	for i := range a.params {
		switch a.params[i].key {
		case "incident":
			a.params[i].value = current["id"]
		case "since", "until":
			a.params[i].value = current[a.params[i].key]
		}
	}
	return a
}

func browserNested(m map[string]json.RawMessage, key string) map[string]json.RawMessage {
	var nested map[string]json.RawMessage
	if json.Unmarshal(m[key], &nested) != nil {
		return nil
	}
	return nested
}

// Editing a query starts at its beginning. A cursor belongs to the old query,
// and retaining it after changing dates could skip results or become invalid.
func browserQueryAction(a action, current map[string]string) action {
	a.params = append([]parameter(nil), a.params...)
	for i := range a.params {
		key := a.params[i].key
		switch key {
		case "before", "before_id", "after", "after_id":
			a.params[i].value = ""
		case "offset":
			a.params[i].value = "0"
		default:
			if value, ok := current[key]; ok {
				a.params[i].value = value
			}
		}
	}
	return a
}

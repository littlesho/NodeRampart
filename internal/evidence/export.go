// SPDX-License-Identifier: MIT

package evidence

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
)

// Export creates one new mode-0600 artifact. Empty format means zip. No browser,
// notification, external command or network access is involved.
func Export(ctx context.Context, bundle Bundle, output, format string) error {
	if format == "" {
		format = "zip"
	}
	if format != "zip" && format != "html" && format != "json" {
		return errors.New("evidence format must be zip, html or json")
	}
	if err := ctx.Err(); err != nil {
		return errors.New("evidence export cancelled")
	}
	if err := bundle.Validate(); err != nil {
		return err
	}
	// Even re-exporting the same IPC result must not reuse its public aliases.
	// Copy mutable members so exporting never changes the caller's snapshot.
	bundle, err := renewAliases(bundle)
	if err != nil {
		return err
	}
	data, err := json.Marshal(bundle)
	if err != nil || len(data)+1 > MaxJSONBytes {
		return errors.New("evidence JSON exceeds output bounds")
	}
	data = append(data, '\n')
	var page []byte
	if format != "json" {
		page, err = renderHTML(ctx, bundle, data)
		if err != nil {
			return err
		}
	}
	file, err := newOutput(output)
	if err != nil {
		return err
	}
	defer file.close()
	out := &boundedWriter{ctx: ctx, out: file.file, remaining: MaxOutputBytes}
	switch format {
	case "json":
		_, err = out.Write(data)
	case "html":
		_, err = out.Write(page)
	case "zip":
		err = writeZIP(out, bundle, page, data)
	}
	if err != nil {
		return errors.New("evidence output could not be completed")
	}
	if err := ctx.Err(); err != nil {
		return errors.New("evidence export cancelled")
	}
	return file.publishContext(ctx)
}

func renewAliases(b Bundle) (Bundle, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return Bundle{}, errors.New("evidence pseudonyms unavailable")
	}
	alias := func(prefix, value string) string {
		if value == "" {
			return ""
		}
		h := hmac.New(sha256.New, key[:])
		h.Write([]byte(prefix))
		h.Write([]byte{0})
		h.Write([]byte(value))
		return prefix + "_" + hex.EncodeToString(h.Sum(nil)[:16])
	}
	copyEvents := func(events []Event) []Event {
		out := append([]Event{}, events...)
		for i := range out {
			e := &out[i]
			e.Alert = projectAlert(e.Kind, e.Alert)
			e.Alias = alias("event", e.Alias)
			e.IncidentAlias = alias("incident", e.IncidentAlias)
			e.SourceAlias = alias("source", e.SourceAlias)
			e.Delivery.NotificationAlias = alias("notification", e.Delivery.NotificationAlias)
			e.Delivery.SilenceAlias = alias("silence", e.Delivery.SilenceAlias)
		}
		return out
	}
	b.Events = copyEvents(b.Events)
	b.RelatedSSH = copyEvents(b.RelatedSSH)
	b.Monitors = append([]Monitor{}, b.Monitors...)
	for i := range b.Monitors {
		b.Monitors[i].IncidentAlias = alias("incident", b.Monitors[i].IncidentAlias)
	}
	if b.Incident != nil {
		incident := *b.Incident
		incident.Alias = alias("incident", incident.Alias)
		b.Incident = &incident
	}
	return b, nil
}

type boundedWriter struct {
	ctx       context.Context
	out       io.Writer
	remaining int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > w.remaining {
		return 0, errors.New("evidence output exceeds byte bound")
	}
	n, err := w.out.Write(p)
	w.remaining -= n
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func writeZIP(out io.Writer, b Bundle, page, data []byte) error {
	archive := zip.NewWriter(out)
	manifest := []byte(fmt.Sprintf("%x  index.html\n%x  evidence.json\n", sha256.Sum256(page), sha256.Sum256(data)))
	for _, entry := range []struct {
		name string
		data []byte
	}{{"index.html", page}, {"evidence.json", data}, {"SHA256SUMS", manifest}} {
		h := &zip.FileHeader{Name: entry.name, Method: zip.Store, Modified: b.SnapshotAt}
		h.SetMode(0o600)
		writer, err := archive.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err := writer.Write(entry.data); err != nil {
			return err
		}
	}
	return archive.Close()
}

var pageTemplate = template.Must(template.New("evidence").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><title>NodeRampart offline evidence</title><style>body{font:16px/1.5 system-ui;max-width:80rem;margin:2rem auto;padding:0 1rem}table{border-collapse:collapse;width:100%}td,th{padding:.4rem;text-align:left;border-bottom:1px solid #aaa}pre{white-space:pre-wrap;overflow-wrap:anywhere}.notice{border-left:4px solid #777;padding:.8rem}</style></head>
<body><h1>NodeRampart offline evidence</h1><p>Format {{.Bundle.FormatVersion}} · {{.Bundle.Kind}}</p><p>Requested UTC period: {{.Bundle.Start}} — {{.Bundle.End}}<br>Database snapshot observation: {{.Bundle.SnapshotAt}}</p><p class="notice">{{.Bundle.Privacy}}</p><p>{{.Bundle.Consistency}}</p><p>Producer: {{.Bundle.Producer.Version}}; commit: {{.Bundle.Producer.Commit}}.</p><p>Rule fingerprint: {{if .Bundle.RuleFingerprint}}{{.Bundle.RuleFingerprint}}{{else}}unavailable{{end}}. {{.Bundle.RuleFingerprintScope}}</p>
{{with .Bundle.Incident}}<h2>Incident {{.Alias}}</h2><p>Retained events in the requested period: {{.RetainedEvents}}; retained start: {{.HasStart}}; retained recovery: {{.HasRecovery}}.</p><p>{{.Correlation}} Context sources truncated: {{.SourcesTruncated}}.</p>{{end}}
<h2>Retained events</h2><p>First {{len .Bundle.Events}} records; further events omitted: {{.Bundle.EventsTruncated}}. Delivery history has independent retention. Alert explanations use inputs saved with that event; missing values are unavailable, not zero or today's rules.</p><table><thead><tr><th>UTC time</th><th>Event</th><th>Phase</th><th>Severity</th><th>Count</th><th>Notification outcome</th><th>Recorded alert explanation</th></tr></thead><tbody>{{range .Bundle.Events}}<tr><td>{{.ObservedAt}}</td><td>{{.Kind}}</td><td>{{.Phase}}</td><td>{{.Severity}}</td><td>{{.Count}}</td><td>{{.Delivery.Decision}} / {{.Delivery.State}}</td><td>{{with .Alert}}{{template "alert" .}}{{else}}—{{end}}</td></tr>{{end}}</tbody></table>
<h2>Related SSH context</h2><p>{{len .Bundle.RelatedSSH}} records; further related records omitted: {{.Bundle.RelatedTruncated}}. Details and aliases appear in the JSON below.</p>
<h2>Coverage and loss</h2><p>{{.Bundle.Coverage.Qualification}}</p><p>Coverage segments truncated: {{.Bundle.Coverage.SegmentsTruncated}}; historical input truncated: {{.Bundle.Coverage.HistoryTruncated}}; gaps truncated: {{.Bundle.Coverage.GapsTruncated}}; loss hours truncated: {{.Bundle.Coverage.LossHoursTruncated}}.</p>
<h2>Retention and compaction</h2><p>{{.Bundle.Retention.Qualification}}</p><p>Detailed entries truncated: {{.Bundle.Retention.Truncated}}; lifetime evicted entries: {{.Bundle.Retention.EvictedEntries}}.</p>
<h2>Complete sanitized JSON</h2><pre>{{.JSON}}</pre></body></html>
{{define "alert"}}<div>Context: {{.Availability}}</div>{{if .Reason}}<div>Reason: {{.Reason}}</div>{{end}}{{if .Period}}<div>Period: {{.Period}}</div>{{end}}{{if .PeriodStart}}<div>UTC bounds: {{.PeriodStart}} — {{.PeriodEnd}}</div>{{end}}{{if .ObservedBytes}}<div>Observed outgoing bytes: {{.ObservedBytes}}</div>{{end}}{{if .ThresholdBytes}}<div>Traffic threshold (bytes): {{.ThresholdBytes}}</div>{{end}}{{if .ObservedCost}}<div>Estimated cost: {{.ObservedCost}} {{.Currency}}</div>{{end}}{{if .ThresholdCost}}<div>Cost threshold: {{.ThresholdCost}} {{.Currency}}</div>{{end}}{{if .Milestone}}<div>Recorded milestone (%): {{.Milestone}}</div>{{end}}{{if .BaselineMeanBytes}}<div>Baseline daily mean (bytes): {{.BaselineMeanBytes}}</div>{{end}}{{if .BaselineDays}}<div>Covered baseline days: {{.BaselineDays}}</div>{{end}}{{if .GrowthRatio}}<div>Growth multiple: {{.GrowthRatio}}</div>{{end}}{{if .Coverage}}<div>Coverage: {{.Coverage}}</div>{{end}}{{if .Basis}}<div>Basis: observed selected-interface guest TX estimate; private traffic and duplicated paths may be included.</div>{{end}}{{if .ConditionSince}}<div>Condition observed since (UTC): {{.ConditionSince}}</div>{{end}}{{end}}
`))

func renderHTML(ctx context.Context, b Bundle, data []byte) ([]byte, error) {
	var output bytes.Buffer
	w := &boundedWriter{ctx: ctx, out: &output, remaining: MaxHTMLBytes}
	if err := pageTemplate.Execute(w, struct {
		Bundle Bundle
		JSON   string
	}{b, string(data)}); err != nil {
		return nil, errors.New("evidence HTML could not be rendered within bounds")
	}
	return output.Bytes(), nil
}

// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/store"
)

const MaxFullBodyBytes = store.MaxReportFullBodyBytes
const maxDocumentEntries = 1024

// Document is independent of the short delivery body. It stores only already
// transformed history, not credentials, and is immutable with its daily row.
type Document struct {
	SchemaVersion int                 `json:"schema_version"`
	Date          string              `json:"date,omitempty"`
	Title         string              `json:"title"`
	Hostname      string              `json:"hostname"`
	Timezone      string              `json:"timezone"`
	PeriodStart   time.Time           `json:"period_start_utc"`
	PeriodEnd     time.Time           `json:"period_end_utc"`
	GeneratedAt   time.Time           `json:"generated_at_utc"`
	Body          string              `json:"body"`
	Summary       store.Summary       `json:"summary"`
	Integrity     store.IntegrityView `json:"integrity"`
	Billing       *billing.Snapshot   `json:"billing,omitempty"`
	Notes         []string            `json:"notes"`
}

func (b *Builder) StructuredRange(ctx context.Context, title string, start, end, asOf time.Time) (*Document, error) {
	work, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body, pricing, err := b.fullRangeWithBilling(work, title, start, end, asOf)
	if err != nil {
		return nil, err
	}
	summary, err := b.Store.Summary(work, start, end, b.TopN)
	if err != nil {
		return nil, err
	}
	integrity, err := b.Store.Integrity(work, store.IntegrityQuery{Start: start, End: end, Limit: 100})
	if err != nil {
		return nil, err
	}
	limit := b.TopN
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		return nil, errors.New("report top_n exceeds entry limit")
	}
	notes := []string{"Retained observations; missing data is not reconstructed. Counts include overlapping UTC hours.", "Queries use bounded snapshots; recent or late observations can change during generation.", "Coverage segments and gaps are limited to 100; attribution display uses configured top_n, at most 50. Running coverage is not proof of lossless capture."}
	if len(summary.Traffic) > limit {
		summary.Traffic = summary.Traffic[:limit]
		notes = append(notes, "Attribution entries omitted by the configured display limit; complete interface totals remain available.")
	}
	if len(summary.Auth)+len(summary.Events)+len(summary.TopSources)+len(summary.Components) > maxDocumentEntries {
		return nil, errors.New("local report exceeds entry limit")
	}
	doc := &Document{SchemaVersion: 1, Title: title, Hostname: b.Hostname, Timezone: b.location().String(), PeriodStart: start.UTC(), PeriodEnd: end.UTC(), GeneratedAt: asOf.UTC(), Body: body, Summary: summary, Integrity: integrity, Billing: pricing, Notes: notes}
	if _, err := EncodeDocument(doc); err != nil {
		return nil, err
	}
	return doc, work.Err()
}

func EncodeDocument(doc *Document) (json.RawMessage, error) {
	if doc == nil || doc.SchemaVersion != 1 || !doc.PeriodStart.Before(doc.PeriodEnd) || doc.GeneratedAt.IsZero() || len(doc.Title) > 256 || len(doc.Hostname) > 256 || doc.Body == "" || len(doc.Body) > MaxFullBodyBytes {
		return nil, errors.New("invalid full report document")
	}
	if len(doc.Summary.Traffic) > 50 || len(doc.Summary.Auth)+len(doc.Summary.Events)+len(doc.Summary.TopSources)+len(doc.Summary.Components) > maxDocumentEntries || len(doc.Integrity.Components) > 128 || len(doc.Integrity.Segments) > 100 || len(doc.Integrity.Gaps) > 100 || len(doc.Integrity.LossHours) > maxDocumentEntries || len(doc.Notes) > 16 {
		return nil, errors.New("local report exceeds entry limit")
	}
	for _, note := range doc.Notes {
		if len(note) > 4096 {
			return nil, errors.New("local report note exceeds byte limit")
		}
	}
	data, err := json.Marshal(doc)
	if err != nil || len(data) > store.MaxReportDocumentBytes {
		return nil, errors.New("local report exceeds document byte limit")
	}
	return data, nil
}

func DecodeDocument(data json.RawMessage) (*Document, error) {
	if len(data) == 0 {
		return nil, errors.New("full content unavailable for this legacy snapshot; original short body preserved")
	}
	if len(data) > store.MaxReportDocumentBytes {
		return nil, errors.New("report document exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var doc Document
	if err := decoder.Decode(&doc); err != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid report document")
	}
	if _, err := EncodeDocument(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func ShortBody(body string) string { return joinWithin(strings.Split(body, "\n"), 4096) }

// NotificationBody does not copy retained source identifiers into a new
// delivery under today's privacy policy. Full detail stays in the local archive.
func NotificationBody(snapshot store.ReportSnapshot) (string, error) {
	lines := []string{"🛡 <b>NodeRampart — daily report " + snapshot.Date + "</b>", "Local archive: " + snapshot.Date}
	if len(snapshot.Document) == 0 {
		return ShortBody(strings.Join(append(lines, "Legacy original full content unavailable; open the preserved local report."), "\n")), nil
	}
	doc, err := DecodeDocument(snapshot.Document)
	if err != nil {
		return "", err
	}
	var events, auth uint64
	for _, item := range doc.Summary.Events {
		events += item.Count
	}
	for _, item := range doc.Summary.Auth {
		auth += item.Count
	}
	lines = append(lines, "Host: "+html.EscapeString(doc.Hostname), fmt.Sprintf("Period: %s — %s", doc.PeriodStart.Format(time.RFC3339), doc.PeriodEnd.Format(time.RFC3339)), fmt.Sprintf("Interface RX %s / TX %s", formatBytes(doc.Summary.Interface.RXBytes), formatBytes(doc.Summary.Interface.TXBytes)), fmt.Sprintf("Sensor batches: %d; kernel drops: %d; IPC loss estimate: %d batches", doc.Summary.Batches, doc.Summary.KernelDrops, doc.Summary.IPCDroppedBatches), "Coverage can be partial or unknown; inspect the full local archive. Source identifiers are omitted from this delivery.")
	lines = append(lines, fmt.Sprintf("Retained security events: %d; SSH observations: %d", events, auth))
	if doc.Billing != nil {
		lines = append(lines, fmt.Sprintf("Cycle guest TX estimate: %.2f %s; not a provider bill.", doc.Billing.Estimate.Cost, doc.Billing.Estimate.Currency))
	}
	return ShortBody(strings.Join(lines, "\n")), nil
}

// SPDX-License-Identifier: MIT

package api

type ReportTrendArgs struct {
	Days int `json:"days"`
}
type ReportExportArgs struct {
	Date   string `json:"date"`
	Format string `json:"format"`
}

type NotifyChannelArgs struct {
	Channel     string `json:"channel,omitempty"`
	ConfirmPaid bool   `json:"confirm_paid,omitempty"`
	PreviewID   string `json:"preview_id,omitempty"`
}

// Reconciliation is an explicit local acknowledgement after restoring a paid
// channel's ledger. It never submits a message or overrides a platform opt-out.
type OfficialReconcileArgs struct {
	Channel     string `json:"channel"`
	EvidenceRef string `json:"evidence_ref"`
	Confirm     bool   `json:"confirm"`
}

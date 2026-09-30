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
	Channel string `json:"channel,omitempty"`
}

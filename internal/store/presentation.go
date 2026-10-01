// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"strings"
	"unicode/utf8"
)

func normalizePresentation(message *OutboxMessage) error {
	if message.Language == "" {
		message.Language = "en"
	}
	if message.Language != "en" && message.Language != "zh" || message.Channel == "webhook" && message.Language != "en" {
		return errors.New("invalid notification presentation language")
	}
	if len(message.Timezone) > 256 || !utf8.ValidString(message.Timezone) || strings.IndexFunc(message.Timezone, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("invalid notification presentation timezone")
	}
	return nil
}

func mergeablePresentation(message OutboxMessage) bool {
	// An empty legacy context is unknown. Local's name and a single offset do
	// not identify its region rules across hosts/restarts. Keep both bodies
	// intact instead of asserting that these presentation contexts are equal.
	return message.Timezone != "" && message.Timezone != "Local" && !strings.HasPrefix(message.Timezone, "Local|")
}

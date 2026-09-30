// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"
)

func TestNotificationDiscardIsolatedCLIIsExplicitAndBounded(t *testing.T) {
	options, err := parseCommand(nil, "notify_discard_isolated", time.Now())
	if err != nil || options.Request.Command != "notify_discard_isolated" {
		t.Fatal("discard isolated command unavailable", err)
	}
	if _, err := parseCommand([]string{"--destination", "telegram:other"}, "notify_discard_isolated", time.Now()); err == nil {
		t.Fatal("discard accepted arbitrary destination scope")
	}
}

// SPDX-License-Identifier: MIT

package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeArgsRejectsAmbiguousShapesAndUnknownFields(t *testing.T) {
	for _, data := range []string{`null`, `[]`, `{"id":"a","synthetic_private_field":true}`, `{"id":"a"} {}`, `{"id":123}`, `{"id":"` + strings.Repeat("a", MaxArgsSize) + `"}`} {
		var args IDArgs
		err := DecodeArgs(json.RawMessage(data), &args)
		if err == nil || strings.Contains(err.Error(), "synthetic_private") {
			t.Fatal("invalid arguments must fail with a redacted error")
		}
	}
	var args IDArgs
	if err := DecodeArgs(json.RawMessage(`{"id":"evt_123"}`), &args); err != nil || args.ID != "evt_123" {
		t.Fatal("valid arguments rejected")
	}
}

func TestArgumentBounds(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("a", 129), "../private", "a\n", "<script>"} {
		if ValidID(id) {
			t.Fatal("unsafe ID accepted")
		}
	}
	for _, date := range []string{"2026-2-01", "2026-02-30", "2026-01-01extra"} {
		if ValidDate(date) {
			t.Fatal("invalid date accepted")
		}
	}
	if !ValidDate("2024-02-29") || ValidLimit(0) || ValidLimit(101) || !ValidLimit(100) {
		t.Fatal("invalid argument bounds")
	}
}

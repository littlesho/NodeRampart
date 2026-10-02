// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
)

func TestOfficialNotifySelectionAndPaidConfirmationAreExplicit(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		for _, command := range []string{"notify_preview", "notify_test", "notify_discard_isolated"} {
			options, err := parseCommand([]string{"--channel", channel}, command, time.Now())
			if err != nil {
				t.Fatal(channel, command, err)
			}
			var args api.NotifyChannelArgs
			if json.Unmarshal(options.Request.Args, &args) != nil || args.Channel != channel || args.ConfirmPaid {
				t.Fatal("selection or confirmation changed")
			}
		}
	}
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, channel := range []string{"twilio_sms", "whatsapp_cloud"} {
		options, err := parseCommand([]string{"--channel", channel, "--confirm-paid", "--preview-id", key}, "notify_test", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var args api.NotifyChannelArgs
		if json.Unmarshal(options.Request.Args, &args) != nil || !args.ConfirmPaid || args.PreviewID != key {
			t.Fatal("confirmation not bound to preview")
		}
	}
	for _, args := range [][]string{
		{"--channel", "line", "--confirm-paid"},
		{"--channel", "twilio_sms", "--preview-id", "invalid"},
		{"--channel", "qqbot", "--preview-id", key},
		{"--channel", "unknown"},
	} {
		if _, err := parseCommand(args, "notify_test", time.Now()); err == nil {
			t.Fatal("unsafe arguments accepted")
		}
	}
}

func TestOfficialDisabledConfigurationDoesNotInspectOrContactCredentials(t *testing.T) {
	cfg := config.Defaults()
	if err := validateOfficialReferences(cfg, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

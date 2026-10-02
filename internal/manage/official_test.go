// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
)

func officialCredentialInput(channel string) map[string]string {
	in := map[string]string{"credential_action": "replace"}
	switch channel {
	case "qqbot":
		in["app_id"], in["app_secret"], in["target_type"], in["target_id"] = "10001", "SYNTHETIC-SECRET-0000", "group", "SYNTHETIC_OPENID_0000"
	case "line":
		in["channel_access_token"], in["target_type"], in["target_id"] = "SYNTHETIC-TOKEN-0000", "user", "U"+strings.Repeat("0", 32)
	case "twilio_sms":
		in["account_sid"], in["auth_mode"], in["auth_token"], in["from"], in["to"] = "AC"+strings.Repeat("0", 32), "auth_token", "SYNTHETIC-TOKEN-0000", "+12025550101", "+12025550102"
	case "whatsapp_cloud":
		in["phone_number_id"], in["access_token"], in["recipient"], in["graph_version"] = "100000001", "SYNTHETIC-TOKEN-0000", "12025550102", config.WhatsAppGraphVersion
		for _, kind := range []string{"event", "daily", "test"} {
			in[kind+"_name"] = "synthetic_notice"
			in[kind+"_language"] = "zh_CN"
			in[kind+"_parameters"] = "host_alias,event_kind,phase,severity,time,bounded_summary,local_reference"
		}
	}
	return in
}

func recordOfficialSubscription(t *testing.T, m *Manager, channel string) {
	t.Helper()
	if _, err := m.Action(context.Background(), channel+"_subscription", map[string]string{"consent_action": "record", "purpose": "合成监控通知", "notification_types": "event,daily,test", "evidence_ref": "synthetic-consent-1", "cost_confirmed": "yes"}); err != nil {
		t.Fatal(err)
	}
}

func TestOfficialManagementIndividualHiddenFieldsAndNoImplicitSend(t *testing.T) {
	m, _ := fixtureManager(t)
	m.Request = func(context.Context, string, any) (json.RawMessage, error) {
		t.Error("local setup contacted daemon notification API")
		return nil, errors.New("unexpected request")
	}
	for _, channel := range config.OfficialChannelNames() {
		input := officialCredentialInput(channel)
		result, err := m.Action(context.Background(), channel+"_credentials", input)
		if err != nil {
			t.Fatal(channel, err)
		}
		recordOfficialSubscription(t, m, channel)
		if _, err := m.Action(context.Background(), channel+"_setup", map[string]string{"enabled": "yes", "language": "zh"}); err != nil {
			t.Fatal(channel, err)
		}
		snapshot, _ := m.Load(context.Background())
		c := snapshot.Config.Notifications.OfficialChannels()[channel]
		if !c.Enabled || c.Language != "zh" || filepath.Dir(c.CredentialFile) != m.localPath("secrets") {
			t.Fatal("wrong local configuration")
		}
		credential, err := m.readOfficialCredential(channel, c.CredentialFile)
		if err != nil || credential.Channel != channel {
			t.Fatal("protected snapshot", err)
		}
		stat, err := os.Stat(c.CredentialFile)
		if err != nil || stat.Mode().Perm() != 0o600 {
			t.Fatal("unsafe credential permissions")
		}
		ordinary, _ := os.ReadFile(m.ConfigPath)
		journal, _ := os.ReadFile(m.journalPath())
		for _, key := range config.OfficialCredentialFieldNames(channel) {
			value := input[key]
			if len(value) < 8 {
				continue
			}
			for _, text := range []string{result, string(ordinary), string(journal)} {
				if strings.Contains(text, value) {
					t.Fatal("credential field leaked", key)
				}
			}
		}
	}
	snapshot, _ := m.Load(context.Background())
	if snapshot.Config.Notifications.Telegram.Enabled || snapshot.Config.Notifications.Webhook.Enabled || snapshot.Config.Heartbeat.Enabled {
		t.Fatal("old channels changed")
	}
	entries, _ := os.ReadDir(m.localPath("secrets"))
	if len(entries) != 4 {
		t.Fatal("one channel deleted another snapshot")
	}
}

func TestOfficialConsentIsExplicitAndKeepRevokeDoNotRotateBasis(t *testing.T) {
	m, _ := fixtureManager(t)
	ctx := context.Background()
	if _, err := m.Action(ctx, "twilio_sms_setup", map[string]string{"enabled": "yes"}); err == nil {
		t.Fatal("enable accepted without consent")
	}
	if _, err := m.Action(ctx, "twilio_sms_subscription", map[string]string{"consent_action": "record", "purpose": "Synthetic alert", "notification_types": "event,test", "evidence_ref": "fixture", "cost_confirmed": "no"}); err == nil {
		t.Fatal("paid consent without cost confirmation")
	}
	recordOfficialSubscription(t, m, "twilio_sms")
	first, _ := m.Load(ctx)
	a := first.Config.Notifications.TwilioSMS.Subscription
	if _, err := m.Action(ctx, "twilio_sms_subscription", map[string]string{"consent_action": "keep"}); err != nil {
		t.Fatal(err)
	}
	kept, _ := m.Load(ctx)
	if !reflect.DeepEqual(a, kept.Config.Notifications.TwilioSMS.Subscription) {
		t.Fatal("keep rotated consent")
	}
	if _, err := m.Action(ctx, "twilio_sms_subscription", map[string]string{"consent_action": "keep", "platform_recovery_confirmed": "yes"}); err == nil {
		t.Fatal("ordinary keep overrode opt-out recovery")
	}
	if _, err := m.Action(ctx, "twilio_sms_subscription", map[string]string{"consent_action": "revoke"}); err != nil {
		t.Fatal(err)
	}
	revoked, _ := m.Load(ctx)
	if !revoked.Config.Notifications.TwilioSMS.Subscription.Revoked || revoked.Config.Notifications.TwilioSMS.Subscription.BasisID != a.BasisID {
		t.Fatal("revoke erased or rotated basis")
	}
	bad := revoked
	bad.Config.Notifications.TwilioSMS.Subscription.Revoked = false
	if _, err := m.Save(ctx, bad); err == nil {
		t.Fatal("ordinary draft changed recipient consent")
	}
	recordOfficialSubscription(t, m, "twilio_sms")
	fresh, _ := m.Load(ctx)
	b := fresh.Config.Notifications.TwilioSMS.Subscription
	if b.BasisID == a.BasisID || b.Revoked || b.PlatformRecoveryConfirmed {
		t.Fatal("new consent basis incorrect")
	}
}

func TestOfficialCredentialKeepPartialReplaceClearAndRecovery(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		t.Run(channel, func(t *testing.T) {
			m, _ := fixtureManager(t)
			ctx := context.Background()
			if _, err := m.Action(ctx, channel+"_credentials", officialCredentialInput(channel)); err != nil {
				t.Fatal(err)
			}
			a, _ := m.Load(ctx)
			path := a.Config.Notifications.OfficialChannels()[channel].CredentialFile
			before, _ := m.readOfficialCredential(channel, path)
			if _, err := m.Action(ctx, channel+"_credentials", map[string]string{"credential_action": "keep"}); err != nil {
				t.Fatal(err)
			}
			b, _ := m.Load(ctx)
			if b.Config.Notifications.OfficialChannels()[channel].CredentialFile != path {
				t.Fatal("keep changed identity")
			}
			key := map[string]string{"qqbot": "app_secret", "line": "channel_access_token", "twilio_sms": "auth_token", "whatsapp_cloud": "access_token"}[channel]
			if _, err := m.Action(ctx, channel+"_credentials", map[string]string{"credential_action": "replace", key: "fixture-0000000000000000"}); err != nil {
				t.Fatal(err)
			}
			c, _ := m.Load(ctx)
			newPath := c.Config.Notifications.OfficialChannels()[channel].CredentialFile
			if newPath == path {
				t.Fatal("uncertain credential rotation reused generation")
			}
			after, err := m.readOfficialCredential(channel, newPath)
			if err != nil {
				t.Fatal(err)
			}
			before.AppSecret, after.AppSecret = "", ""
			before.ChannelAccessToken, after.ChannelAccessToken = "", ""
			before.AuthToken, after.AuthToken = "", ""
			before.AccessToken, after.AccessToken = "", ""
			if !reflect.DeepEqual(before, after) {
				t.Fatal("partial replacement lost unmodified fields")
			}
			if _, err := m.Action(ctx, channel+"_credentials", map[string]string{"credential_action": "replace", "clear_fields": key}); err == nil {
				t.Fatal("cleared required credential was accepted")
			}
			cancel, cancelNow := context.WithCancel(ctx)
			cancelNow()
			if _, err := m.Action(cancel, channel+"_credentials", map[string]string{"credential_action": "replace", key: "SYNTHETIC-CANCELLED-0000"}); err == nil {
				t.Fatal("cancelled apply succeeded")
			}
			unchanged, _ := m.Load(ctx)
			if unchanged.Config.Notifications.OfficialChannels()[channel].CredentialFile != newPath {
				t.Fatal("cancelled apply changed reference")
			}
			if _, err := m.Action(ctx, channel+"_credentials", map[string]string{"credential_action": "clear"}); err != nil {
				t.Fatal(err)
			}
			cleared, _ := m.Load(ctx)
			if cleared.Config.Notifications.OfficialChannels()[channel].CredentialFile != "" {
				t.Fatal("clear kept reference")
			}
			entries, _ := os.ReadDir(m.localPath("secrets"))
			if len(entries) != 0 {
				t.Fatal("owned stale snapshots survived explicit clear")
			}
		})
	}
}

func TestOfficialUnsafeFilesAndArgumentsNeverEchoValues(t *testing.T) {
	m, _ := fixtureManager(t)
	ctx := context.Background()
	in := officialCredentialInput("line")
	if _, err := m.Action(ctx, "line_credentials", in); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Load(ctx)
	path := s.Config.Notifications.LINE.CredentialFile
	for _, mode := range []os.FileMode{0o644, 0o660, 0o666, 0o600 | os.ModeSetuid, 0o600 | os.ModeSetgid, 0o600 | os.ModeSticky} {
		if os.Chmod(path, mode) != nil {
			t.Fatal("fixture")
		}
		_, err := m.readOfficialCredential("line", path)
		if err == nil || strings.Contains(err.Error(), in["channel_access_token"]) || strings.Contains(err.Error(), path) {
			t.Fatal("unsafe credential accepted or echoed")
		}
	}
	if os.Chmod(path, 0o600) != nil {
		t.Fatal("fixture")
	}
	if os.Link(path, path+".link") != nil {
		t.Fatal("fixture")
	}
	if _, err := m.readOfficialCredential("line", path); err == nil {
		t.Fatal("hardlink accepted")
	}
	outside := filepath.Join(t.TempDir(), "line.json")
	data, _ := os.ReadFile(path)
	if os.WriteFile(outside, data, 0o600) != nil {
		t.Fatal("fixture")
	}
	if _, err := m.readOfficialCredential("line", outside); err == nil {
		t.Fatal("outside managed directory accepted")
	}
	if _, err := m.Action(ctx, "line_credentials", map[string]string{"credential_action": "replace", "unsupported": "SYNTHETIC-SECRET-0000"}); err == nil || strings.Contains(err.Error(), "SYNTHETIC-SECRET-0000") {
		t.Fatal("unknown argument echoed")
	}
}

func TestOfficialCredentialParentsAreValidatedBeforeEnabledSave(t *testing.T) {
	for _, directory := range []string{"secrets", "configuration"} {
		for _, mode := range []os.FileMode{0o770, 0o757, 0o1777} {
			t.Run(directory+"-"+mode.String(), func(t *testing.T) {
				m, services := fixtureManager(t)
				ctx := context.Background()
				input := officialCredentialInput("line")
				if _, err := m.Action(ctx, "line_credentials", input); err != nil {
					t.Fatal(err)
				}
				recordOfficialSubscription(t, m, "line")
				snapshot, err := m.Load(ctx)
				if err != nil {
					t.Fatal(err)
				}
				snapshot.Config.Notifications.LINE.Enabled = true
				path := snapshot.Config.Notifications.LINE.CredentialFile
				if _, err := m.readOfficialCredential("line", path); err != nil {
					t.Fatal("safe service-readable parent rejected", err)
				}
				parent := filepath.Dir(path)
				if directory == "configuration" {
					parent = filepath.Dir(m.ConfigPath)
				}
				if err := os.Chmod(parent, mode); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(parent, 0o750)
				for _, validate := range []func() error{
					func() error { _, err := m.readOfficialCredential("line", path); return err },
					func() error { return m.preflight(snapshot.Config) },
				} {
					err := validate()
					if err == nil || strings.Contains(err.Error(), path) || strings.Contains(err.Error(), input["channel_access_token"]) {
						t.Fatal("unsafe credential parent accepted or echoed")
					}
				}
				before, _ := os.ReadFile(m.ConfigPath)
				calls := len(services.calls)
				if _, err := m.Save(ctx, snapshot); err == nil {
					t.Fatal("enabled configuration saved with an unsafe credential parent")
				}
				after, _ := os.ReadFile(m.ConfigPath)
				if string(after) != string(before) || len(services.calls) != calls {
					t.Fatal("rejected save changed configuration or service state")
				}
			})
		}
	}
}

func TestOfficialPaidPreviewAndConfirmationArguments(t *testing.T) {
	m, _ := fixtureManager(t)
	calls := 0
	m.Request = func(_ context.Context, command string, args any) (json.RawMessage, error) {
		calls++
		v, ok := args.(api.NotifyChannelArgs)
		if !ok || v.Channel != "twilio_sms" {
			t.Fatal("wrong channel args")
		}
		if command == "notify_test" && (!v.ConfirmPaid || v.PreviewID != strings.Repeat("0", 64)) {
			t.Fatal("confirmation lost")
		}
		return json.RawMessage(`{"state":"queued"}`), nil
	}
	if _, err := m.Action(context.Background(), "notify_preview", map[string]string{"channel": "twilio_sms"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(context.Background(), "notify_test", map[string]string{"channel": "twilio_sms", "confirm_paid": "yes", "preview_id": strings.Repeat("0", 64)}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("unexpected requests")
	}
}

func TestOfficialRecoveryRequiresNewConsentEvidence(t *testing.T) {
	m, _ := fixtureManager(t)
	ctx := context.Background()
	recordOfficialSubscription(t, m, "twilio_sms")
	before, _ := m.Load(ctx)
	old := before.Config.Notifications.TwilioSMS.Subscription
	in := map[string]string{"consent_action": "record", "purpose": "Synthetic restored SMS alerts", "notification_types": "event,test", "evidence_ref": old.EvidenceRef, "cost_confirmed": "yes", "platform_recovery_confirmed": "yes"}
	if _, err := m.Action(ctx, "twilio_sms_subscription", in); err == nil {
		t.Fatal("old evidence accepted for recipient recovery")
	}
	unchanged, _ := m.Load(ctx)
	if !reflect.DeepEqual(old, unchanged.Config.Notifications.TwilioSMS.Subscription) {
		t.Fatal("rejected declaration changed subscription")
	}
	in["evidence_ref"] = "synthetic-recovery-consent-2"
	if _, err := m.Action(ctx, "twilio_sms_subscription", in); err != nil {
		t.Fatal(err)
	}
	after, _ := m.Load(ctx)
	fresh := after.Config.Notifications.TwilioSMS.Subscription
	if fresh.BasisID == old.BasisID || fresh.ConfirmedAt <= old.ConfirmedAt || fresh.EvidenceRef == old.EvidenceRef || !fresh.PlatformRecoveryConfirmed {
		t.Fatal("new consent declaration did not preserve recovery requirements")
	}
}

func TestOfficialCredentialClearPreservesFilesNotOwnedByProduct(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		t.Run(channel, func(t *testing.T) {
			m, _ := fixtureManager(t)
			ctx := context.Background()
			if _, err := m.Action(ctx, channel+"_credentials", officialCredentialInput(channel)); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(m.localPath("secrets"), channel+"-user.secret")
			body := []byte("SYNTHETIC USER OWNED CREDENTIAL")
			if err := os.WriteFile(foreign, body, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Action(ctx, channel+"_credentials", map[string]string{"credential_action": "clear"}); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(foreign)
			if err != nil || string(got) != string(body) {
				t.Fatal("explicit clear deleted user-owned file", err)
			}
			entries, err := os.ReadDir(m.localPath("secrets"))
			if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(foreign) {
				t.Fatal("clear retained an owned generation or changed foreign file", err)
			}
		})
	}
}

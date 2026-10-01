// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCredentialRotationRollbackRetainsPreviousSnapshot(t *testing.T) {
	for _, channel := range []string{"feishu", "slack"} {
		t.Run(channel, func(t *testing.T) {
			m, services := fixtureManager(t)
			ctx := context.Background()
			if _, err := m.Action(ctx, channel+"_setup", nativeSetupInput(channel)); err != nil {
				t.Fatal(err)
			}
			before, _ := m.Load(ctx)
			old := before.Config.Notifications.NativeChannels()[channel]
			services.active["noderampartd.service"] = true
			services.restartFailures = 1
			if _, err := m.Action(ctx, channel+"_setup", nativeSetupInput(channel)); err == nil {
				t.Fatal("fixture activation did not fail")
			}
			after, err := m.Load(ctx)
			if err != nil || after.Config.Notifications.NativeChannels()[channel] != old {
				t.Fatal("rollback changed original settings", err)
			}
			if _, err := m.readNativeCredential(channel, old.CredentialFile); err != nil {
				t.Fatal("rollback removed original credential", err)
			}
			if _, err := os.Stat(m.journalPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("completed rollback retained recovery journal")
			}
			entries, err := os.ReadDir(m.localPath("secrets"))
			if err != nil || len(entries) != 1 {
				t.Fatal("failed unreferenced replacement was retained", err)
			}
		})
	}
}

func TestNativeUnsafeExistingFeishuSecretCannotBeSilentlyClearedByKeep(t *testing.T) {
	m, _ := fixtureManager(t)
	ctx := context.Background()
	if _, err := m.Action(ctx, "feishu_setup", nativeSetupInput("feishu")); err != nil {
		t.Fatal(err)
	}
	before, _ := m.Load(ctx)
	path := before.Config.Notifications.Feishu.CredentialFile
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	input := nativeSetupInput("feishu")
	input["secret_action"] = "keep"
	delete(input, "secret")
	if _, err := m.Action(ctx, "feishu_setup", input); err == nil {
		t.Fatal("unsafe kept signing secret silently discarded")
	}
	after, _ := m.Load(ctx)
	if after.Config.Notifications.Feishu != before.Config.Notifications.Feishu {
		t.Fatal("failed keep changed credentials")
	}
}

func TestNativeManagementCredentialReaderRejectsAmbiguousAndUnsafeFiles(t *testing.T) {
	m, _ := fixtureManager(t)
	path := m.localPath("synthetic.credential.json")
	valid := `{"url":"` + nativeFixtureURL("feishu") + `","secret":"SYNTHETIC-SIGNING-SECRET"}`
	for _, text := range []string{
		`{"url":"` + nativeFixtureURL("feishu") + `","url":"` + nativeFixtureURL("feishu") + `"}`,
		`{"url":"` + nativeFixtureURL("feishu") + `","secret":null}`,
		`{"url":"` + nativeFixtureURL("feishu") + `","secret":123}`,
		`{"url":"` + nativeFixtureURL("feishu") + `","token":"SYNTHETIC-SECRET"}`,
		`{"secret":"SYNTHETIC-SECRET"}`,
		valid + ` {}`,
		strings.Repeat("x", 8193),
	} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := m.readNativeCredential("feishu", path); err == nil || strings.Contains(err.Error(), "SYNTHETIC") {
			t.Fatal("unsafe document accepted or echoed", err)
		}
	}
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readNativeCredential("feishu", path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readNativeCredential("feishu", path); err == nil {
		t.Fatal("publicly readable credential accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	linked := m.localPath("linked.credential.json")
	if err := os.Link(path, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readNativeCredential("feishu", path); err == nil {
		t.Fatal("hardlinked credential accepted")
	}
	if err := os.Remove(linked); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readNativeCredential("feishu", linked); err == nil {
		t.Fatal("symlink credential accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside.credential.json")
	if err := os.WriteFile(outside, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readNativeCredential("feishu", outside); err == nil {
		t.Fatal("credential outside managed directory read")
	}
}

func TestNativeFirstClearDoesNotCreateCredentialOrSend(t *testing.T) {
	m, _ := fixtureManager(t)
	if _, err := m.Action(context.Background(), "slack_setup", map[string]string{"enabled": "no", "credential_action": "clear"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.localPath("secrets")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty clear created secret directory")
	}
}

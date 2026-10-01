// SPDX-License-Identifier: MIT

package console

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func openReportTimezone(t *testing.T, s *recordedScreen) string {
	t.Helper()
	selectIndex(s, 2)
	awaitFrame(t, s, "Configuration · schema")
	selectIndex(s, 5)
	awaitFrame(t, s, "NodeRampart — Daily reports")
	selectIndex(s, 2)
	return awaitFrame(t, s, "Choose report timezone")
}

func reviewDraft(t *testing.T, s *recordedScreen) string {
	t.Helper()
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "unsaved edits")
	selectIndex(s, len(groups)+2)
	return awaitFrame(t, s, "Review configuration changes")
}

func TestSimulationTimezoneAliasCancelEmptySearchAndNoImplicitSelection(t *testing.T) {
	b := newBackend()
	b.cfg.Reports.Timezone = "US/Eastern"
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	frame := openReportTimezone(t, s)
	if !strings.Contains(frame, "Current configuration: US/Eastern") {
		t.Fatal("selector does not show the exact configured alias")
	}
	if !strings.Contains(frame, "Current:") {
		t.Fatal("current alias is not visibly marked")
	}
	textKeys(s, "synthetic-zone-with-no-match")
	awaitFrame(t, s, "No matching timezones")
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "No matching timezones")
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "NodeRampart — Daily reports")
	selectIndex(s, 2)
	awaitFrame(t, s, "Choose report timezone")
	// Enter on an empty query keeps the highlighted current alias, rather than
	// selecting the first common/canonical timezone in the directory.
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "NodeRampart — Daily reports")
	key(s, tcell.KeyEscape)
	frame = awaitFrame(t, s, "Configuration · schema")
	if strings.Contains(frame, "unsaved edits") {
		t.Fatal("cancel/empty results/current selection changed the draft")
	}
	if b.cfg.Reports.Timezone != "US/Eastern" || len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("timezone browsing mutated backend state or contacted an action")
	}
}

func TestSimulationTimezoneSearchDiffConfirmationAndExactSave(t *testing.T) {
	b := newBackend()
	b.cfg.Reports.Timezone = "Asia/Calcutta"
	b.cfg.Notifications.Telegram.Language = "zh"
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	openReportTimezone(t, s)
	textKeys(s, "上海")
	awaitFrame(t, s, "China · Beijing / Shanghai")
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "NodeRampart — Daily reports")
	frame := reviewDraft(t, s)
	if !strings.Contains(frame, "reports.timezone") || !strings.Contains(frame, `"Asia/Calcutta" → "Asia/Shanghai"`) {
		t.Fatal("review did not retain exact old alias and candidate name")
	}
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Confirm action")
	if len(b.saves) != 0 {
		t.Fatal("timezone change saved before confirmation")
	}
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Configuration saved")
	select {
	case saved := <-b.saves:
		if saved.Config.Reports.Timezone != "Asia/Shanghai" || saved.Config.Notifications.Telegram.Language != "zh" || saved.Fingerprint != "synthetic-fingerprint" {
			t.Fatal("confirmed save lost timezone, independent language or fingerprint")
		}
	case <-time.After(time.Second):
		t.Fatal("missing confirmed save")
	}
	if len(b.calls) != 0 {
		t.Fatal("timezone selection performed an external action")
	}
}

func TestSimulationTimezoneMissingRulesNeverSubstitutesUTC(t *testing.T) {
	b := newBackend()
	b.cfg.Reports.Timezone = "Fixture/No_Rules"
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	openReportTimezone(t, s)
	textKeys(s, "Fixture/No_Rules")
	awaitFrame(t, s, "rules unavailable")
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Timezone rules unavailable")
	if len(b.saves) != 0 || len(b.calls) != 0 || b.cfg.Reports.Timezone != "Fixture/No_Rules" {
		t.Fatal("missing rules changed the configuration or contacted backend")
	}
}

func TestSimulationTelegramSetupPreservesConfiguredLanguageAndNoTestSend(t *testing.T) {
	b := newBackend()
	b.cfg.Notifications.Telegram.Language = "zh"
	b.cfg.Notifications.Telegram.ChatID = "12345"
	b.cfg.Notifications.Telegram.TokenFile = "/synthetic/unchanged.token"
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	frame := openTelegram(t, s)
	if !strings.Contains(frame, "简体中文") {
		t.Fatal("English UI changed the configured Chinese message language")
	}
	if !strings.Contains(frame, "12345") || !strings.Contains(frame, "no") {
		t.Fatal("setup did not prefill the existing target/enablement")
	}
	for range 4 {
		key(s, tcell.KeyTab)
	}
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Confirm action")
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Settings were applied successfully")
	select {
	case call := <-b.calls:
		if call.id != "telegram_setup" || call.args["language"] != "zh" || call.args["token"] != "" || call.args["chat_id"] != "12345" || call.args["enabled"] != "no" {
			t.Fatalf("existing language/target not preserved: %#v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("missing setup action")
	}
	if len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("setup triggered an automatic test message or unrelated save")
	}
}

func TestTelegramLanguageGenericEditorUsesSameConfigField(t *testing.T) {
	b := newBackend()
	b.cfg.Notifications.Telegram.Language = "zh"
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 2)
	awaitFrame(t, s, "Configuration · schema")
	selectIndex(s, 4)
	awaitFrame(t, s, "NodeRampart — Notifications")
	languageIndex := 0
	for _, f := range fields {
		if f.group != "notifications" {
			continue
		}
		if f.path == "notifications.telegram.language" {
			break
		}
		languageIndex++
	}
	selectIndex(s, languageIndex)
	frame := awaitFrame(t, s, "NodeRampart — Edit setting")
	if !strings.Contains(frame, "简体中文") {
		t.Fatalf("configured language missing in generic editor: %s", frame)
	}
	key(s, tcell.KeyEnter)
	key(s, tcell.KeyUp)
	key(s, tcell.KeyEnter)
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "NodeRampart — Notifications")
	frame = reviewDraft(t, s)
	if !strings.Contains(frame, "notifications.telegram.language") || !strings.Contains(frame, `"zh" → "en"`) {
		t.Fatal("generic language editor does not use the shared persisted field")
	}
	if len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("editing notification language altered a target or sent a message")
	}
}

func TestSimulationEmptyTimezoneSearchDoesNotWriteFirstCatalogName(t *testing.T) {
	// Real configuration validation rejects this explicit empty value. The UI
	// still must not turn a backend with no current selection into a first-item
	// write merely because Enter was pressed in the empty search field.
	b := newBackend()
	b.cfg.Reports.Timezone = ""
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	openReportTimezone(t, s)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Choose report timezone")
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "NodeRampart — Daily reports")
	key(s, tcell.KeyEscape)
	frame := awaitFrame(t, s, "Configuration · schema")
	if strings.Contains(frame, "unsaved edits") || len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("empty search selected and wrote the first directory entry")
	}
}

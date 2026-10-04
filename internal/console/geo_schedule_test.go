// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func openGeoSchedule(t *testing.T, s *recordedScreen, language string) string {
	t.Helper()
	selectIndex(s, 6)
	awaitFrame(t, s, navigationText(language, "NodeRampart — Local GeoIP", "NodeRampart — 本地 GeoIP"))
	selectIndex(s, 3)
	return awaitFrame(t, s, navigationText(language, "NodeRampart — Daily GeoIP updates", "NodeRampart — GeoIP 每日更新"))
}

func requireGeoChoice(t *testing.T, frame, label, want string) {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		if _, value, ok := strings.Cut(line, label); ok {
			if strings.TrimSpace(value) != want {
				t.Fatalf("%s: got %q, want %q", label, strings.TrimSpace(value), want)
			}
			return
		}
	}
	t.Fatalf("missing %q in frame:\n%s", label, frame)
}

func requireGeoCall(t *testing.T, b *fakeBackend, id string) backendCall {
	t.Helper()
	select {
	case call := <-b.calls:
		if call.id != id {
			t.Fatalf("unexpected action %q, want %q", call.id, id)
		}
		return call
	case <-time.After(time.Second):
		t.Fatalf("missing action %q", id)
		return backendCall{}
	}
}

func stopGeoSession(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("GeoIP terminal session did not exit")
	}
}

func TestSimulationGeoScheduleSurvivesReopenAndNewSession(t *testing.T) {
	for _, language := range []string{"en", "zh"} {
		t.Run(language, func(t *testing.T) {
			var saved atomic.Bool
			b := newBackend()
			b.action = func(_ context.Context, id string, args map[string]string) (string, error) {
				switch id {
				case "geo_schedule_state":
					return fmt.Sprintf("{\"enabled\":%t}", saved.Load()), nil
				case "geo_schedule":
					saved.Store(args["enabled"] == "yes")
					return "Schedule saved.", nil
				default:
					return "", errors.New("unexpected GeoIP action")
				}
			}
			label := navigationText(language, "Daily update schedule", "每日更新计划")
			for _, want := range []bool{true, false} {
				s, cancel, done := launchWithLanguage(t, false, language, b)
				awaitFrame(t, s, navigationText(language, "Main menu", "主菜单"))
				before := "no"
				if saved.Load() {
					before = "yes"
				}
				requireGeoChoice(t, openGeoSchedule(t, s, language), label, before)
				requireGeoCall(t, b, "geo_schedule_state")
				key(s, tcell.KeyEnter)
				if want {
					key(s, tcell.KeyDown)
				} else {
					key(s, tcell.KeyUp)
				}
				key(s, tcell.KeyEnter)
				key(s, tcell.KeyTab)
				key(s, tcell.KeyEnter)
				awaitFrame(t, s, navigationText(language, "Confirm action", "确认操作"))
				key(s, tcell.KeyRight)
				key(s, tcell.KeyEnter)
				awaitFrame(t, s, "Schedule saved.")
				call := requireGeoCall(t, b, "geo_schedule")
				value := "no"
				if want {
					value = "yes"
				}
				if call.args["enabled"] != value || saved.Load() != want {
					t.Fatalf("save did not preserve selection: %#v", call)
				}
				key(s, tcell.KeyEscape)
				awaitFrame(t, s, navigationText(language, "NodeRampart — Local GeoIP", "NodeRampart — 本地 GeoIP"))
				selectIndex(s, 3)
				frame := awaitFrame(t, s, navigationText(language, "NodeRampart — Daily GeoIP updates", "NodeRampart — GeoIP 每日更新"))
				requireGeoChoice(t, frame, label, value)
				requireGeoCall(t, b, "geo_schedule_state")
				key(s, tcell.KeyEscape)
				awaitFrame(t, s, navigationText(language, "NodeRampart — Local GeoIP", "NodeRampart — 本地 GeoIP"))
				if len(b.calls) != 0 || len(b.saves) != 0 {
					t.Fatal("viewing or cancelling changed configuration")
				}
				stopGeoSession(t, cancel, done)
			}
			s, cancel, done := launchWithLanguage(t, false, language, b)
			awaitFrame(t, s, navigationText(language, "Main menu", "主菜单"))
			requireGeoChoice(t, openGeoSchedule(t, s, language), label, "no")
			requireGeoCall(t, b, "geo_schedule_state")
			stopGeoSession(t, cancel, done)
		})
	}
}

func TestSimulationGeoDownloadPrefillsScheduleWithoutTermsOrCredentials(t *testing.T) {
	for _, language := range []string{"en", "zh"} {
		for _, setup := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/setup=%t", language, setup), func(t *testing.T) {
				b := newBackend()
				b.action = func(context.Context, string, map[string]string) (string, error) {
					return "{\"enabled\":true}", nil
				}
				s, _, _ := launchWithLanguage(t, setup, language, b)
				if setup {
					awaitFrame(t, s, navigationText(language, "Welcome", "欢迎"))
					selectIndex(s, 2)
				} else {
					awaitFrame(t, s, navigationText(language, "Main menu", "主菜单"))
					selectIndex(s, 6)
					awaitFrame(t, s, navigationText(language, "NodeRampart — Local GeoIP", "NodeRampart — 本地 GeoIP"))
					selectIndex(s, 1)
				}
				label := navigationText(language, "Enable daily database updates", "启用每日数据库更新")
				frame := awaitFrame(t, s, label)
				requireGeoChoice(t, frame, label, "yes")
				requireGeoChoice(t, frame, navigationText(language, "I have accepted the GeoLite terms", "我已接受 GeoLite 条款"), "no")
				requireGeoChoice(t, frame, "MaxMind Account ID", "")
				requireGeoChoice(t, frame, "MaxMind License Key", "")
				requireGeoCall(t, b, "geo_schedule_state")
				key(s, tcell.KeyEscape)
				if setup {
					awaitFrame(t, s, navigationText(language, "Welcome", "欢迎"))
				} else {
					awaitFrame(t, s, navigationText(language, "NodeRampart — Local GeoIP", "NodeRampart — 本地 GeoIP"))
				}
				if len(b.calls) != 0 || len(b.saves) != 0 {
					t.Fatal("opening or skipping GeoIP setup performed a mutation")
				}
			})
		}
	}
}

func TestSimulationGeoDownloadRereadsScheduleAfterHelp(t *testing.T) {
	for _, language := range []string{"en", "zh"} {
		t.Run(language, func(t *testing.T) {
			var enabled atomic.Bool
			enabled.Store(true)
			b := newBackend()
			b.action = func(context.Context, string, map[string]string) (string, error) {
				return fmt.Sprintf("{\"enabled\":%t}", enabled.Load()), nil
			}
			s, _, _ := launchWithLanguage(t, false, language, b)
			awaitFrame(t, s, navigationText(language, "Main menu", "主菜单"))
			selectIndex(s, 6)
			awaitFrame(t, s, navigationText(language, "NodeRampart — Local GeoIP", "NodeRampart — 本地 GeoIP"))
			selectIndex(s, 1)
			label := navigationText(language, "Enable daily database updates", "启用每日数据库更新")
			requireGeoChoice(t, awaitFrame(t, s, label), label, "yes")
			requireGeoCall(t, b, "geo_schedule_state")
			// Move past the four fields and Continue/Cancel to Read full help.
			for range 6 {
				key(s, tcell.KeyTab)
			}
			key(s, tcell.KeyEnter)
			awaitFrame(t, s, "1 / 1")
			enabled.Store(false) // Simulate an external systemctl change.
			key(s, tcell.KeyEscape)
			frame := awaitFrame(t, s, label)
			requireGeoChoice(t, frame, label, "no")
			requireGeoChoice(t, frame, navigationText(language, "I have accepted the GeoLite terms", "我已接受 GeoLite 条款"), "no")
			requireGeoChoice(t, frame, "MaxMind Account ID", "")
			requireGeoChoice(t, frame, "MaxMind License Key", "")
			requireGeoCall(t, b, "geo_schedule_state")
			if len(b.calls) != 0 || len(b.saves) != 0 {
				t.Fatal("returning from GeoIP help performed a mutation")
			}
		})
	}
}

func TestSimulationGeoScheduleReadFailureNeverMeansDisabled(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		err            error
	}{
		{"backend error", "{\"enabled\":false}", errors.New("SYNTHETIC_PRIVATE_ERROR")},
		{"invalid JSON", "SYNTHETIC_PRIVATE_RESPONSE", nil},
		{"missing state", "{}", nil},
		{"null state", "{\"enabled\":null}", nil},
		{"wrong type", "{\"enabled\":\"no\"}", nil},
		{"trailing JSON", "{\"enabled\":false} true", nil},
		{"unexpected fields", "{\"enabled\":false,\"private\":\"SYNTHETIC_PRIVATE_RESPONSE\"}", nil},
		{"duplicate state", "{\"enabled\":true,\"enabled\":false}", nil},
		{"duplicate state reversed", "{\"enabled\":false,\"enabled\":true}", nil},
		{"duplicate identical state", "{\"enabled\":false,\"enabled\":false}", nil},
		{"case variant", "{\"Enabled\":false}", nil},
		{"array", "[{\"enabled\":false}]", nil},
		{"oversized", strings.Repeat(" ", 4096) + "{\"enabled\":false}", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBackend()
			b.action = func(context.Context, string, map[string]string) (string, error) {
				return tc.response, tc.err
			}
			s, _, _ := launch(t, false, b)
			awaitFrame(t, s, "Main menu")
			selectIndex(s, 6)
			awaitFrame(t, s, "NodeRampart — Local GeoIP")
			selectIndex(s, 3)
			frame := awaitFrame(t, s, "GeoIP schedule unavailable")
			if strings.Contains(frame, "SYNTHETIC_PRIVATE") || !strings.Contains(frame, "No settings were changed") {
				t.Fatal("schedule read failure was misleading or exposed raw backend data")
			}
			requireGeoCall(t, b, "geo_schedule_state")
			if len(b.calls) != 0 || len(b.saves) != 0 {
				t.Fatal("failed state read caused a mutation")
			}
		})
	}
}

func TestSimulationGeoScheduleReadCancellationDoesNotMutate(t *testing.T) {
	b := newBackend()
	b.action = func(ctx context.Context, _ string, _ map[string]string) (string, error) {
		<-ctx.Done()
		return "{\"enabled\":false}", nil
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 6)
	awaitFrame(t, s, "NodeRampart — Local GeoIP")
	selectIndex(s, 3)
	awaitFrame(t, s, "Working")
	requireGeoCall(t, b, "geo_schedule_state")
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "GeoIP schedule unavailable")
	if len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("cancelled state read caused a mutation")
	}
}

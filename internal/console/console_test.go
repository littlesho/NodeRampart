// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/rivo/tview"
)

func TestEveryConfigLeafHasAnEditor(t *testing.T) {
	leaves := map[string]bool{}
	var visit func(reflect.Type, string)
	visit = func(kind reflect.Type, prefix string) {
		for i := 0; i < kind.NumField(); i++ {
			f := kind.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			path := prefix + name
			if f.Type.Kind() == reflect.Struct && f.Type != reflect.TypeOf(config.Duration{}) {
				visit(f.Type, path+".")
			} else {
				leaves[path] = true
			}
		}
	}
	visit(reflect.TypeOf(config.Config{}), "")
	delete(leaves, "schema_version")
	for _, f := range fields {
		if !leaves[f.path] {
			t.Fatalf("duplicate or unknown editor %s", f.path)
		}
		delete(leaves, f.path)
		if f.en == "" || f.zh == "" || f.helpEN == "" || f.helpZH == "" {
			t.Fatalf("missing bilingual help for %s", f.path)
		}
	}
	if len(leaves) != 0 {
		t.Fatalf("settings absent from the UI: %v", leaves)
	}
	cfg := config.Defaults()
	cfg.Sensor.Interfaces = []string{"lab4", "lab6"}
	cfg.Privacy.StoreIP = "full"
	cfg.Storage.MinFreeBytes = 42
	cfg.Paths.ControlSocket = "/run/noderampart/custom.sock"
	before := cfg
	before.Sensor.Interfaces = append([]string(nil), cfg.Sensor.Interfaces...)
	for _, f := range fields {
		if err := setField(&cfg, f.path, fieldText(&cfg, f.path)); err != nil {
			t.Fatalf("%s: %v", f.path, err)
		}
	}
	if !reflect.DeepEqual(cfg, before) {
		t.Fatal("opening and keeping settings lost advanced values")
	}
	for _, bad := range []string{"NaN", "+Inf", "-Inf"} {
		if setField(&cfg, "detection.recovery_ratio", bad) == nil {
			t.Fatal("accepted a non-finite ratio")
		}
	}
	if setField(&cfg, "schema_version", "99") == nil || setField(&cfg, "hostname", "bad\x1b[31m") == nil {
		t.Fatal("accepted schema mutation or terminal controls")
	}
	if err := setField(&cfg, "detection.bytes_per_second", "18446744073709551615"); err != nil || cfg.Detection.BytesPerSecond != ^uint64(0) {
		t.Fatal("integer precision was lost")
	}
}

func TestOutputIsBoundedAndSanitizedAfterHTMLDecode(t *testing.T) {
	pages, cut := outputPages("<b>hello</b>&#27;[31m&#x9b;31m\u202e[red]literal[-]" + strings.Repeat("中文", 100000))
	if !cut || len(pages) < 2 {
		t.Fatal("expected bounded, paginated output")
	}
	joined := strings.Join(pages, "")
	if !strings.Contains(joined, "[red]literal[-]") || !strings.Contains(joined, "hello") {
		t.Fatal("literal text lost")
	}
	for _, p := range pages {
		if !utf8.ValidString(p) || len(p) > pageBytes {
			t.Fatal("invalid UTF-8 page boundary")
		}
	}
	for _, r := range joined {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.Is(unicode.Cf, r) {
			t.Fatal("terminal or format controls survived HTML decoding")
		}
	}
	malformed, limited := outputPages(strings.Repeat("\xffa", maxOutputBytes/2))
	if !limited || len(strings.Join(malformed, "")) > maxOutputBytes {
		t.Fatal("UTF-8 replacement expanded output beyond its byte budget")
	}
}

func TestHumanResultsKeepExactCountersAndUnknownFields(t *testing.T) {
	text := humanResult(`{"tx_bytes":18446744073709551615,"future_flag":true,"report":"<b>hello</b>&#27;[31m"}`, "zh")
	if !strings.Contains(text, "发送字节数: 18446744073709551615") || !strings.Contains(text, "future flag: 是") {
		t.Fatal("human rendering dropped data or lost integer precision")
	}
	if !strings.Contains(cleanText(text), "hello") || strings.Contains(cleanText(text), "\x1b") {
		t.Fatal("decoded result was not sanitized")
	}
}

type backendCall struct {
	id   string
	args map[string]string
}
type fakeBackend struct {
	cfg    config.Config
	calls  chan backendCall
	saves  chan Snapshot
	action func(context.Context, string, map[string]string) (string, error)
}

func (b *fakeBackend) Load(context.Context) (Snapshot, error) {
	return Snapshot{Config: b.cfg, Fingerprint: "synthetic-fingerprint"}, nil
}
func (b *fakeBackend) Save(_ context.Context, s Snapshot) (string, error) {
	b.saves <- s
	return "Saved synthetic settings", nil
}
func (b *fakeBackend) Action(ctx context.Context, id string, args map[string]string) (string, error) {
	copyArgs := map[string]string{}
	for k, v := range args {
		copyArgs[k] = v
	}
	b.calls <- backendCall{id, copyArgs}
	if b.action != nil {
		return b.action(ctx, id, copyArgs)
	}
	return "[red]literal[-]\n<b>Safe result</b>&#27;[31m", nil
}

// Capture immutable frames during Show; tcell's GetContents returns its live
// backing slice, so reading it from the test goroutine would itself be a race.
type recordedScreen struct {
	tcell.SimulationScreen
	mu            sync.Mutex
	frames        chan string
	width, height int
}

func newRecordedScreen() *recordedScreen {
	return &recordedScreen{SimulationScreen: tcell.NewSimulationScreen("UTF-8"), frames: make(chan string, 1), width: 100, height: 34}
}
func (s *recordedScreen) Init() error {
	if err := s.SimulationScreen.Init(); err != nil {
		return err
	}
	s.SimulationScreen.SetSize(s.width, s.height)
	return nil
}
func (s *recordedScreen) Show() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SimulationScreen.Show()
	cells, w, h := s.SimulationScreen.GetContents()
	var out strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			runes := cells[y*w+x].Runes
			if len(runes) > 0 {
				out.WriteString(string(runes))
			} else {
				out.WriteByte(' ')
			}
			_, _, _, width := s.SimulationScreen.GetContent(x, y)
			if width > 1 {
				x += width - 1
			}
		}
		out.WriteByte('\n')
	}
	select {
	case <-s.frames:
	default:
	}
	s.frames <- out.String()
}
func (s *recordedScreen) Fini() { s.mu.Lock(); defer s.mu.Unlock(); s.SimulationScreen.Fini() }
func (s *recordedScreen) resize(w, h int) {
	s.mu.Lock()
	s.SimulationScreen.SetSize(w, h)
	s.mu.Unlock()
	_ = s.PostEvent(tcell.NewEventResize(w, h))
}

func awaitFrame(t *testing.T, s *recordedScreen, want string) string {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	var last string
	for {
		select {
		case last = <-s.frames:
			if strings.Contains(last, want) {
				return last
			}
		case <-deadline.C:
			t.Fatalf("screen did not show %q; last frame:\n%s", want, last)
		}
	}
}
func key(s *recordedScreen, k tcell.Key) { s.InjectKey(k, 0, tcell.ModNone) }
func textKeys(s *recordedScreen, text string) {
	for _, r := range text {
		s.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
}
func selectIndex(s *recordedScreen, index int) {
	key(s, tcell.KeyHome)
	for i := 0; i < index; i++ {
		key(s, tcell.KeyDown)
	}
	key(s, tcell.KeyEnter)
}
func launch(t *testing.T, setup bool, b *fakeBackend) (*recordedScreen, context.CancelFunc, <-chan error) {
	t.Helper()
	screen := newRecordedScreen()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	completed := make(chan struct{})
	go func() { done <- Run(ctx, b, Options{Setup: setup, Language: "en", Screen: screen}); close(completed) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-completed:
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			default:
			}
		case <-time.After(time.Second):
			t.Error("terminal goroutine failed to exit")
		}
	})
	return screen, cancel, done
}
func newBackend() *fakeBackend {
	return &fakeBackend{cfg: config.Defaults(), calls: make(chan backendCall, 20), saves: make(chan Snapshot, 10)}
}

func TestSimulationWelcomeSkipsExternalFeaturesAndChangesLanguage(t *testing.T) {
	b := newBackend()
	s, _, _ := launch(t, true, b)
	awaitFrame(t, s, "Welcome")
	selectIndex(s, 5)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 10)
	awaitFrame(t, s, "主菜单")
	s.resize(48, 18)
	awaitFrame(t, s, "当前运行状态")
	select {
	case call := <-b.calls:
		t.Fatalf("setup contacted backend unexpectedly: %s", call.id)
	default:
	}
}

func openTelegram(t *testing.T, s *recordedScreen) {
	t.Helper()
	selectIndex(s, 5)
	awaitFrame(t, s, "Telegram and notifications")
	selectIndex(s, 0)
	awaitFrame(t, s, "Bot token (hidden)")
}

func TestSimulationSecretIsMaskedAndCancelDoesNotApply(t *testing.T) {
	b := newBackend()
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	openTelegram(t, s)
	value := "SYNTHETIC_PRIVATE_VALUE_ONLY"
	textKeys(s, value)
	frame := awaitFrame(t, s, strings.Repeat("•", len(value)))
	if strings.Contains(frame, value) {
		t.Fatal("secret rendered in clear text")
	}
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "Telegram and notifications")
	selectIndex(s, 0)
	frame = awaitFrame(t, s, "Bot token (hidden)")
	if strings.Contains(frame, "••") {
		t.Fatal("cancelled secret was prefilled")
	}
	select {
	case <-b.calls:
		t.Fatal("cancelling settings invoked an action")
	default:
	}
}

func TestSimulationSecretFailureDoesNotEchoBackendError(t *testing.T) {
	value := "SYNTHETIC_PRIVATE_VALUE_ONLY"
	b := newBackend()
	b.action = func(_ context.Context, id string, args map[string]string) (string, error) {
		if id != "telegram_setup" || args["token"] != value {
			t.Error("wrong Telegram setup payload")
		}
		return "", errors.New("unexpected response: " + value)
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	openTelegram(t, s)
	textKeys(s, value)
	key(s, tcell.KeyTab)
	textKeys(s, "12345")
	key(s, tcell.KeyTab)
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	frame := awaitFrame(t, s, "Confirm action")
	if strings.Contains(frame, value) {
		t.Fatal("secret leaked into confirmation")
	}
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	frame = awaitFrame(t, s, "Secret values are not shown")
	if strings.Contains(frame, value) {
		t.Fatal("secret leaked through backend error")
	}
	select {
	case call := <-b.calls:
		if call.id != "telegram_setup" {
			t.Fatal("setup sent a test notification")
		}
	case <-time.After(time.Second):
		t.Fatal("missing setup action")
	}
	select {
	case <-b.calls:
		t.Fatal("extra external action")
	default:
	}
}

func TestSimulationConfigSavePreservesAdvancedSettingsAndRequiresConfirmation(t *testing.T) {
	b := newBackend()
	b.cfg.Hostname = "fixture"
	b.cfg.Sensor.Interfaces = []string{"lab4", "lab6"}
	b.cfg.Reports.Timezone = "UTC"
	b.cfg.Paths.ControlSocket = "/run/noderampart/custom.sock"
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 2)
	awaitFrame(t, s, "Configuration · schema")
	selectIndex(s, 0)
	awaitFrame(t, s, "Host identity")
	selectIndex(s, 0)
	awaitFrame(t, s, "Edit setting")
	for range len("fixture") {
		key(s, tcell.KeyBackspace2)
	}
	textKeys(s, "new-host")
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Host identity")
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "unsaved edits")
	selectIndex(s, len(groups)+1)
	frame := awaitFrame(t, s, "Review configuration changes")
	if !strings.Contains(frame, `"fixture" → "new-host"`) {
		t.Fatal("missing old/new configuration diff")
	}
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Confirm action")
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "unsaved edits")
	select {
	case <-b.saves:
		t.Fatal("save happened before confirmation")
	default:
	}
	selectIndex(s, len(groups)+1)
	awaitFrame(t, s, "Review configuration changes")
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Confirm action")
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Configuration saved")
	select {
	case snapshot := <-b.saves:
		if snapshot.Fingerprint != "synthetic-fingerprint" || snapshot.Config.Hostname != "new-host" || !reflect.DeepEqual(snapshot.Config.Sensor.Interfaces, b.cfg.Sensor.Interfaces) || snapshot.Config.Paths.ControlSocket != b.cfg.Paths.ControlSocket || snapshot.Config.Reports.Timezone != "UTC" {
			t.Fatal("save lost settings, fingerprint or edited value")
		}
	case <-time.After(time.Second):
		t.Fatal("missing confirmed save")
	}
}

func TestSimulationSlowActionCanBeCancelledWithoutBlockingUI(t *testing.T) {
	b := newBackend()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	b.action = func(ctx context.Context, _ string, _ map[string]string) (string, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return "", ctx.Err()
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 0)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("action did not start")
	}
	key(s, tcell.KeyEscape)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Escape did not cancel the backend action")
	}
	awaitFrame(t, s, "not completed")
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "Main menu")
}

func TestSimulationLiteralOutputAndPageNavigation(t *testing.T) {
	b := newBackend()
	b.action = func(context.Context, string, map[string]string) (string, error) {
		return "[red]literal[-]\n" + strings.Repeat("line\n", 9000), nil
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 0)
	frame := awaitFrame(t, s, "1 / 3")
	if !strings.Contains(frame, "[red]literal[-]") {
		t.Fatal("backend text injected tview color markup")
	}
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "2 / 3")
	key(s, tcell.KeyTab)
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "3 / 3")
}

func TestSimulationBusyQuitWaitsForCancellationRecovery(t *testing.T) {
	b := newBackend()
	cancelled := make(chan struct{})
	started := make(chan struct{})
	recoveryDone := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(recoveryDone) }) }
	defer release()
	b.action = func(ctx context.Context, _ string, _ map[string]string) (string, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-recoveryDone
		return "", ctx.Err()
	}
	s, _, done := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 0)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("operation did not start")
	}
	key(s, tcell.KeyCtrlC)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("exit did not cancel operation")
	}
	awaitFrame(t, s, "Waiting for operation recovery")
	select {
	case <-done:
		t.Fatal("terminal exited while backend recovery was still running")
	default:
	}
	key(s, tcell.KeyCtrlC)
	awaitFrame(t, s, "Waiting for operation recovery")
	select {
	case <-done:
		t.Fatal("repeated quit bypassed backend recovery")
	default:
	}
	release()
	awaitFrame(t, s, "not completed")
	key(s, tcell.KeyCtrlC)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal did not exit")
	}
}

func TestSimulationOCIUsesFixedRegionAndExplicitPricingAssumptions(t *testing.T) {
	b := newBackend()
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 7)
	awaitFrame(t, s, "Cloud egress estimates")
	selectIndex(s, 1)
	awaitFrame(t, s, "Choose a provider")
	selectIndex(s, 1)
	awaitFrame(t, s, "OCI geographic group")
	for range 4 {
		key(s, tcell.KeyTab)
	}
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Confirm action")
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Safe result")
	select {
	case call := <-b.calls:
		if call.id != "prices_fetch" || call.args["provider"] != "oci" || call.args["region"] != "north-america-europe-uk" || call.args["free_gb"] != "0" || call.args["unit_bytes"] != "1073741824" {
			t.Fatalf("unexpected price request: %#v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("missing price request")
	}
}

func TestSimulationBackfillFailureRetainsProgress(t *testing.T) {
	b := newBackend()
	b.action = func(context.Context, string, map[string]string) (string, error) {
		return `{"next_date":"2026-09-11","completed":1}`, context.DeadlineExceeded
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 3)
	awaitFrame(t, s, "Reports")
	selectIndex(s, 3)
	awaitFrame(t, s, "First date")
	textKeys(s, "2026-09-10")
	key(s, tcell.KeyTab)
	textKeys(s, "2026-09-11")
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Confirm action")
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	frame := awaitFrame(t, s, "Resume from date: 2026-09-11")
	if !strings.Contains(frame, "completed: 1") || !strings.Contains(frame, "deadline exceeded") {
		t.Fatal("partial backfill progress was discarded")
	}
}

func TestGeoTermsAndDestructiveWordsGateBackendCalls(t *testing.T) {
	b := newBackend()
	u := &ui{app: tview.NewApplication(), backend: b, lang: "en"}
	for _, id := range []string{"geo_download", "uninstall", "purge", "recover_config"} {
		a, _ := findAction(id)
		args := map[string]string{"accepted_terms": "no", "confirm": "wrong", "license_key": "SYNTHETIC"}
		u.prepareAction(a, args, func() {})
		if len(args) != 0 {
			t.Fatal("rejected request kept sensitive arguments")
		}
	}
	select {
	case <-b.calls:
		t.Fatal("rejected request reached backend")
	default:
	}
}

func TestTerminalLocalePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, all, ctype, lang string
		override, reject       bool
	}{
		{"C overrides LANG", "C", "", "en_US.UTF-8", true, false},
		{"POSIX", "POSIX", "", "", true, false},
		{"CTYPE overrides LANG", "", "C", "zh_CN.UTF-8", true, false},
		{"UTF8 overrides CTYPE", "C.UTF-8", "C", "", false, false},
		{"C utf8", "C.utf8", "", "", false, false},
		{"English UTF8", "", "", "en_US.UTF-8", false, false},
		{"Chinese UTF8", "", "", "zh_CN.UTF-8", false, false},
		{"CTYPE UTF8", "", "en_US.UTF-8", "en_US.ISO-8859-1", false, false},
		{"modifier", "en_US.UTF-8@modifier", "", "", false, false},
		{"unset", "", "", "", false, false},
		{"language only", "", "", "en_US", false, false},
		{"legacy ALL", "en_US.ISO-8859-1", "", "zh_CN.UTF-8", false, true},
		{"legacy CTYPE", "", "en_US.ISO-8859-1", "zh_CN.UTF-8", false, true},
		{"explicit ASCII", "", "", "C.US-ASCII", false, true},
		{"unsupported", "", "", "en_US.unknown", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			override, err := terminalLocaleNeedsUTF8(tc.all, tc.ctype, tc.lang)
			if override != tc.override || (err != nil) != tc.reject {
				t.Fatalf("override=%v, error=%v", override, err)
			}
			if err != nil && !strings.Contains(err.Error(), "UTF-8") {
				t.Fatal("missing readable encoding diagnostic")
			}
		})
	}
}

func TestChineseTextAtOutputLimit(t *testing.T) {
	text := strings.Repeat("中", maxOutputBytes/3+1)
	pages, cut := outputPages(text)
	if !cut || strings.Join(pages, "") != strings.Repeat("中", maxOutputBytes/3) {
		t.Fatal("valid Chinese was replaced at the byte limit")
	}
	if cleanText("中文说明 → SSH") != "中文说明 → SSH" {
		t.Fatal("text sanitization changed legitimate Chinese or symbols")
	}
	// Malformed input consisting only of continuation bytes must remain bounded.
	_, _ = outputPages(strings.Repeat("\x80", maxOutputBytes+1))
}

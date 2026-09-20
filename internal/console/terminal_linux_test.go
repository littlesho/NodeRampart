// SPDX-License-Identifier: MIT

package console

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/rivo/tview"
	"golang.org/x/sys/unix"
)

// Cursor/style sequences may separate adjacent Chinese glyphs in a redraw.
// Strip only terminal controls when matching text; verify the captured wire
// bytes are valid UTF-8 separately. This cannot turn ASCII '?' into Chinese.
var terminalControls = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]|\x1b\\][^\x1b\x07]*(?:\x07|\x1b\\\\)|\x1b[()][0-2A-Z]")

// Each child owns a real controlling PTY and tcell encoder/decoder. Stdin is
// /dev/null, so these tests also guard against reading bootstrap's script pipe.
func TestTerminalUTF8PTY(t *testing.T) {
	for _, tc := range []struct {
		name, all, ctype, lang, language, mode, wantError string
	}{
		{"C over UTF8", "C", "", "en_US.UTF-8", "en", "setup", ""},
		{"POSIX", "POSIX", "", "", "zh", "tui", ""},
		{"CTYPE C", "", "C", "en_US.UTF-8", "zh", "setup", ""},
		{"C UTF8", "C.UTF-8", "C", "", "en", "tui", ""},
		{"C utf8", "C.utf8", "", "", "zh", "setup", ""},
		{"English locale Chinese UI", "", "", "en_US.UTF-8", "zh", "tui", ""},
		{"Chinese locale English UI", "", "", "zh_CN.UTF-8", "en", "setup", ""},
		{"unset locales", "", "", "", "en", "tui", ""},
		{"legacy locale", "en_US.ISO-8859-1", "", "zh_CN.UTF-8", "zh", "setup", "requires UTF-8"},
		{"unknown charset", "en_US.unknown", "", "", "en", "tui", "requires UTF-8"},
		{"unsupported TERM", "C", "", "", "en", "badterm", "supported controlling terminal"},
		{"no controlling tty", "C", "", "", "en", "notty", "controlling terminal"},
		{"Chinese input", "C", "", "en_US.UTF-8", "zh", "input", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
			if err != nil {
				t.Fatal("real PTY unavailable: ", err)
			}
			defer master.Close()
			if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
				t.Fatal(err)
			}
			n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
			if err != nil {
				t.Fatal(err)
			}
			slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer slave.Close()
			if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 120}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTerminalProcess$", "-test.timeout=12s")
			cmd.WaitDelay = time.Second
			for _, item := range os.Environ() {
				key, _, _ := strings.Cut(item, "=")
				if key != "LANG" && key != "LC_ALL" && key != "LC_CTYPE" && key != "TERM" && !strings.HasPrefix(key, "NR_") && !strings.HasPrefix(key, "NODERAMPART_") {
					cmd.Env = append(cmd.Env, item)
				}
			}
			cmd.Env = append(cmd.Env, "TERM=xterm", "NR_CONSOLE_PTY_CHILD=1", "NR_PTY_MODE="+tc.mode, "NR_PTY_LANGUAGE="+tc.language, "NR_PTY_ERROR="+tc.wantError)
			for key, value := range map[string]string{"LC_ALL": tc.all, "LC_CTYPE": tc.ctype, "LANG": tc.lang} {
				if value != "" {
					cmd.Env = append(cmd.Env, key+"="+value)
				}
			}
			cmd.ExtraFiles = []*os.File{slave}
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: tc.mode != "notty", Ctty: 3}
			var process bytes.Buffer
			cmd.Stdout, cmd.Stderr = &process, &process
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			slave.Close()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			// The timeout kills and reaps the child even if an assertion fails.
			waited := false
			defer func() {
				cancel()
				if !waited {
					<-done
				}
			}()
			type interaction struct{ text, keys string }
			var actions []interaction
			if tc.wantError == "" {
				if tc.mode == "input" {
					actions = []interaction{{"编辑设置", "\x15中文主机\t\r"}, {"中文主机", "\x03"}}
				} else {
					en, zh, toggle := "Main menu", "主菜单", "\x1bOF\x1bOA\r"
					if tc.mode == "setup" {
						en, zh, toggle = "Welcome", "欢迎", "\x1bOF\r"
					}
					first, second := en, zh
					if tc.language == "zh" {
						first, second = zh, en
					}
					actions = []interaction{{first, toggle}, {second, toggle}, {first, "\x03"}}
				}
			}
			var output []byte
			phase, start := 0, 0
			for !waited {
				select {
				case err := <-done:
					waited = true
					if err != nil {
						t.Fatalf("terminal child: %v; phase %d/%d; tail %q; process %.300s", err, phase, len(actions), terminalControls.ReplaceAll(output[max(0, len(output)-500):], nil), process.String())
					}
				default:
				}
				poll := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
				if _, err := unix.Poll(poll, 25); err != nil && err != unix.EINTR {
					t.Fatal(err)
				}
				if poll[0].Revents&unix.POLLIN != 0 {
					var buf [8192]byte
					n, err := master.Read(buf[:])
					if err != nil && err != syscall.EIO {
						t.Fatal(err)
					}
					output = append(output, buf[:n]...)
					if len(output) > 256<<10 {
						t.Fatal("terminal output exceeded test bound")
					}
				}
				if phase < len(actions) && bytes.Contains(terminalControls.ReplaceAll(output[start:], nil), []byte(actions[phase].text)) {
					if _, err := master.Write([]byte(actions[phase].keys)); err != nil {
						t.Fatal(err)
					}
					start = len(output)
					phase++
				}
			}
			if phase != len(actions) || !utf8.Valid(output) {
				t.Fatalf("incomplete UTF-8 interaction: %d/%d, bytes=%d", phase, len(actions), len(output))
			}
			if tc.wantError == "" {
				words := []string{"中文", "↑", "—", "·"}
				switch tc.mode {
				case "setup":
					words = append(words, "欢迎", "基本设置", "网络")
				case "tui":
					words = append(words, "主菜单", "当前运行状态", "采集")
				case "input":
					words = append(words, "编辑设置", "主机显示名称", "中文主机")
				}
				for _, word := range words {
					if !bytes.Contains(terminalControls.ReplaceAll(output, nil), []byte(word)) {
						t.Fatalf("real UTF-8 output lacks %q", word)
					}
				}
			}
		})
	}
}

func TestTerminalProcess(t *testing.T) {
	if os.Getenv("NR_CONSOLE_PTY_CHILD") != "1" {
		return
	}
	locale := func() map[string]string {
		m := map[string]string{}
		for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
			if value, ok := os.LookupEnv(key); ok {
				m[key] = value
			}
		}
		return m
	}
	before := locale()
	defer func() {
		if !reflect.DeepEqual(before, locale()) {
			t.Error("screen initialization changed the caller's locale")
		}
	}()
	mode := os.Getenv("NR_PTY_MODE")
	if mode == "badterm" {
		t.Setenv("TERM", "noderampart-nonexistent-terminal-fixture")
	}
	backend := &fakeBackend{cfg: config.Defaults(), calls: make(chan backendCall, 1), saves: make(chan Snapshot, 1)}
	if mode == "input" {
		screen, err := newTerminalScreen()
		if err != nil {
			t.Fatal(err)
		}
		screen = &alreadyInitialized{Screen: screen}
		defer screen.Fini()
		u := &ui{app: tview.NewApplication(), screen: screen, backend: backend, ctx: context.Background(), lang: "zh", snapshot: Snapshot{Config: backend.cfg}, loaded: true, changed: map[string]bool{}}
		u.app.SetScreen(screen)
		for _, f := range fields {
			if f.path == "hostname" {
				u.editField(f)
			}
		}
		u.app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
			if ev.Key() == tcell.KeyCtrlC {
				u.app.Stop()
				return nil
			}
			return ev
		})
		if err := u.app.Run(); err != nil {
			t.Fatal(err)
		}
		if u.snapshot.Config.Hostname != "中文主机" || !u.dirty {
			t.Fatal("Chinese input did not reach the draft intact")
		}
	} else {
		err := Run(context.Background(), backend, Options{Setup: mode == "setup", Language: os.Getenv("NR_PTY_LANGUAGE")})
		want := os.Getenv("NR_PTY_ERROR")
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Fatalf("terminal result: %v; expected diagnostic %q", err, want)
		}
	}
	if len(backend.calls) != 0 || len(backend.saves) != 0 {
		t.Fatal("terminal test performed a backend action or save")
	}
}

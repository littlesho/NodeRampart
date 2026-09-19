// SPDX-License-Identifier: MIT

// Package console provides the local terminal interface. The backend owns
// authorization, file transactions and service changes; UI callbacks never run
// privileged commands or read credentials directly.
package console

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/rivo/tview"
)

type Snapshot struct {
	Config      config.Config
	Fingerprint string
}

type Backend interface {
	Load(context.Context) (Snapshot, error)
	Save(context.Context, Snapshot) (string, error)
	Action(context.Context, string, map[string]string) (string, error)
}

type Options struct {
	Setup    bool
	Language string
	Screen   tcell.Screen
}

const maxOutputBytes = 256 << 10
const pageBytes = 16 << 10

type ui struct {
	app                 *tview.Application
	screen              tcell.Screen
	backend             Backend
	ctx                 context.Context
	lang                string
	snapshot            Snapshot
	baseline            config.Config
	loaded, dirty, busy bool
	changed             map[string]bool
	back                func()
	results             chan func()
	cancelWork          context.CancelFunc
}

// alreadyInitialized avoids tview.SetScreen's unchecked second Init call.
type alreadyInitialized struct {
	tcell.Screen
	finalized sync.Once
}

func (*alreadyInitialized) Init() error { return nil }
func (s *alreadyInitialized) Fini()     { s.finalized.Do(s.Screen.Fini) }

func Run(ctx context.Context, backend Backend, options Options) error {
	if backend == nil {
		return errors.New("terminal backend is unavailable")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	screen := options.Screen
	if screen == nil {
		// tcell's Unix default opens /dev/tty, never bootstrap's piped stdin.
		var err error
		screen, err = tcell.NewTerminfoScreen()
		if err != nil {
			return errors.New("a supported controlling terminal is required; use an interactive SSH session")
		}
	}
	if err := screen.Init(); err != nil {
		screen.Fini()
		return errors.New("could not initialize the controlling terminal")
	}
	screen = &alreadyInitialized{Screen: screen}
	defer screen.Fini()
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	u := &ui{app: tview.NewApplication(), screen: screen, backend: backend, ctx: workCtx,
		lang: options.Language, changed: map[string]bool{}, results: make(chan func(), 1)}
	if !strings.HasPrefix(u.lang, "zh") {
		u.lang = "en"
	} else {
		u.lang = "zh"
	}
	u.app.SetScreen(screen)
	u.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		select {
		case result := <-u.results:
			result()
		default:
		}
		switch event.Key() {
		case tcell.KeyF24:
			return nil // Internal completion wake-up, no user action.
		case tcell.KeyCtrlC:
			u.quit()
			return nil
		case tcell.KeyEscape:
			if u.back != nil {
				u.back()
			} else {
				u.quit()
			}
			return nil
		}
		return event
	})
	ready := make(chan struct{})
	var once sync.Once
	u.app.SetBeforeDrawFunc(func(tcell.Screen) bool { once.Do(func() { close(ready) }); return false })
	if options.Setup {
		u.welcome()
	} else {
		u.home()
	}
	// Cancellation must not call Stop before Run has established its event loop.
	finished, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ready:
		case <-workCtx.Done():
			return
		case <-finished:
			return
		}
		select {
		case <-workCtx.Done():
			u.app.Stop()
		case <-finished:
		}
	}()
	err := u.app.Run()
	close(finished)
	cancel()
	<-watcherDone
	if err != nil {
		return errors.New("terminal session ended unexpectedly")
	}
	return nil
}

func (u *ui) tr(en, zh string) string {
	if u.lang == "zh" {
		return zh
	}
	return en
}

func (u *ui) root(title string, content tview.Primitive, back func()) {
	u.back = back
	header := tview.NewTextView().SetDynamicColors(false).SetText("NodeRampart — " + cleanText(title))
	footer := tview.NewTextView().SetDynamicColors(false).SetText(u.tr("↑↓ Navigate · Enter Select · Tab Next · Esc Back · Ctrl-C Quit", "↑↓ 移动 · Enter 选择 · Tab 下一项 · Esc 返回 · Ctrl-C 退出"))
	frame := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(header, 2, 0, false).AddItem(content, 0, 1, true).AddItem(footer, 1, 0, false)
	u.app.SetRoot(frame, true).SetFocus(content)
}

type menuItem struct {
	en, zh, detailEN, detailZH string
	selectItem                 func()
}

func (u *ui) menu(en, zh string, items []menuItem, back func()) {
	list := tview.NewList().ShowSecondaryText(true)
	for _, item := range items {
		list.AddItem(tview.Escape(cleanText(u.tr(item.en, item.zh))), tview.Escape(cleanText(u.tr(item.detailEN, item.detailZH))), 0, item.selectItem)
	}
	u.root(u.tr(en, zh), list, back)
}

func (u *ui) welcome() {
	u.menu("Welcome — set up your server observer", "欢迎 — 设置服务器观察工具", []menuItem{
		{"Basic settings", "基本设置", "Network and SSH observation work without external accounts.", "网络与 SSH 观察无需外部账户。", u.configuration},
		{"Set up Telegram (optional)", "设置 Telegram（可选）", "Use your bot token and chat ID. Setup sends no message.", "填写自己的 Bot Token 与会话 ID；设置不会发送消息。", func() { u.openAction("telegram_setup", u.welcome) }},
		{"Download local GeoIP (optional)", "下载本地 GeoIP（可选）", "Requires your MaxMind account, license key and terms acceptance.", "需要自己的 MaxMind 账户、License Key 与条款同意。", func() { u.openAction("geo_download", u.welcome) }},
		{"Set up cloud egress estimates (optional)", "设置云出站费用估算（可选）", "Public tariffs only; no cloud credentials needed.", "仅使用公开价格；不需要云账户凭据。", u.prices},
		{"Start configured services", "启动已配置的服务", "Choose this when configuration is ready.", "完成配置后选择此项。", func() { u.openAction("service_start", u.welcome) }},
		{"Skip optional setup / open menu", "跳过可选设置 / 打开主菜单", "You can return to every setting later.", "之后可随时设置所有功能。", u.home},
		{"中文 / English", "English / 中文", "Switch interface language.", "切换界面语言。", func() { u.toggleLanguage(); u.welcome() }},
	}, u.home)
}

func (u *ui) toggleLanguage() {
	if u.lang == "zh" {
		u.lang = "en"
	} else {
		u.lang = "zh"
	}
}

func (u *ui) home() {
	u.menu("Main menu", "主菜单", []menuItem{
		{"Current status", "当前运行状态", "Collection, storage, interfaces and notification health.", "采集、存储、网卡与通知健康状态。", func() { u.openAction("status", u.home) }},
		{"Health and diagnosis", "完整性与诊断", "Coverage gaps and local service checks.", "数据覆盖缺口与本机服务检查。", u.health},
		{"Configuration", "功能配置", "Edit every setting; review before saving.", "编辑所有设置；确认后再保存。", u.configuration},
		{"Reports", "报告", "Current report, saved days and bounded backfill.", "当前报告、已保存日报与有界历史补齐。", u.reports},
		{"Events and incidents", "事件与 Incident", "Timeline and related event details.", "时间线与相关事件详情。", u.incidents},
		{"Telegram and notifications", "Telegram 与通知", "Bot setup, delivery results and expiring silences.", "Bot 设置、投递结果与到期静默。", u.notifications},
		{"Local GeoIP databases", "本地 GeoIP 数据库", "Download, inspect and schedule optional updates.", "下载、查看和设置可选定时更新。", u.geo},
		{"Cloud egress cost estimates", "云公网出站费用估算", "AWS, OCI or a custom tariff; not a provider invoice.", "AWS、OCI 或自定义价格；不代表云厂商账单。", u.prices},
		{"Backup, replay and privacy", "备份、回放与隐私", "Local files and privacy key management.", "本地文件与隐私密钥管理。", u.tools},
		{"Services and uninstall", "服务与卸载", "Start, stop, recover configuration or uninstall.", "启动、停止、恢复配置或卸载。", u.services},
		{"中文 / English", "English / 中文", "Switch interface language.", "切换界面语言。", func() { u.toggleLanguage(); u.home() }},
		{"Exit", "退出", "Close this interface; running services keep their state.", "关闭界面；服务保持当前状态。", u.quit},
	}, nil)
}

func (u *ui) quit() {
	if u.busy {
		if u.cancelWork != nil {
			u.cancelWork()
		}
		u.output(u.tr("Waiting for operation recovery", "等待操作恢复完成"), u.tr("Cancellation requested. Wait for the current operation and any configuration recovery to finish before exiting. Completed changes may already have taken effect.", "已请求取消。请等待当前操作及可能的配置恢复完成后再退出；已完成的修改可能已经生效。"), u.quit)
		return
	}
	if u.dirty {
		u.confirm(u.tr("Discard unsaved configuration edits and exit?", "放弃未保存的配置修改并退出？"), func() { u.app.Stop() }, u.home)
		return
	}
	u.app.Stop()
}

func (u *ui) confirm(message string, yes, no func()) {
	modal := tview.NewModal().SetText(tview.Escape(cleanText(message))).AddButtons([]string{u.tr("Cancel", "取消"), u.tr("Confirm", "确认")})
	modal.SetDoneFunc(func(index int, _ string) {
		if index == 1 {
			yes()
		} else {
			no()
		}
	})
	u.root(u.tr("Confirm action", "确认操作"), modal, no)
}

func (u *ui) output(title, text string, back func()) {
	pages, truncated := outputPages(text)
	u.outputPage(title, pages, 0, truncated, back)
}

func (u *ui) outputPage(title string, pages []string, index int, truncated bool, back func()) {
	u.outputPageAction(title, pages, index, truncated, "", nil, back)
}

func (u *ui) outputPageAction(title string, pages []string, index int, truncated bool, label string, action func(), back func()) {
	view := tview.NewTextView().SetDynamicColors(false).SetRegions(false).SetWordWrap(true).SetScrollable(true).SetText(pages[index])
	view.SetBorder(true).SetTitle(fmt.Sprintf(" %d / %d ", index+1, len(pages)))
	buttons := tview.NewForm().SetHorizontal(true)
	if index > 0 {
		buttons.AddButton(u.tr("Previous page", "上一页"), func() { u.outputPageAction(title, pages, index-1, truncated, label, action, back) })
	}
	if index+1 < len(pages) {
		buttons.AddButton(u.tr("Next page", "下一页"), func() { u.outputPageAction(title, pages, index+1, truncated, label, action, back) })
	}
	if action != nil {
		buttons.AddButton(label, action)
	}
	buttons.AddButton(u.tr("Back", "返回"), back)
	if truncated {
		title += u.tr(" (output limited to 256 KiB)", "（输出限制为 256 KiB）")
	}
	flex := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 3, 0, false)
	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab {
			if view.HasFocus() {
				index := 0
				if event.Key() == tcell.KeyBacktab {
					index = buttons.GetButtonCount() - 1
				}
				buttons.SetFocus(index)
				u.app.SetFocus(buttons)
			} else {
				_, index := buttons.GetFocusedItemIndex()
				if event.Key() == tcell.KeyTab && index == buttons.GetButtonCount()-1 || event.Key() == tcell.KeyBacktab && index == 0 {
					u.app.SetFocus(view)
				} else {
					return event
				}
			}
			return nil
		}
		return event
	})
	u.root(title, flex, back)
}

// background permits one finite backend operation at a time. Completion crosses
// a bounded channel and is applied only by the UI input loop, avoiding both
// widget races and QueueUpdate goroutines stranded after the terminal exits.
func (u *ui) background(title string, work func(context.Context) func(), back func()) {
	if u.busy {
		u.output(title, u.tr("An operation is still finishing. Wait before starting another.", "仍有操作正在结束，请稍后重试。"), back)
		return
	}
	u.busy = true
	ctx, cancel := context.WithTimeout(u.ctx, 5*time.Minute)
	u.cancelWork = cancel
	u.output(title, u.tr("Working… Esc requests cancellation. Completed changes may already have taken effect.", "正在处理… 按 Esc 请求取消；已完成的修改可能已经生效。"), func() {
		cancel()
		u.output(title, u.tr("Cancellation requested. Waiting for the current operation to finish.", "已请求取消，等待当前操作结束。"), u.home)
	})
	go func() {
		result := work(ctx)
		cancel()
		select {
		case u.results <- func() { u.busy = false; u.cancelWork = nil; result() }:
			_ = u.screen.PostEvent(tcell.NewEventKey(tcell.KeyF24, 0, tcell.ModNone))
		case <-u.ctx.Done():
		}
	}()
}

func cleanText(text string) string {
	text = strings.NewReplacer("<b>", "", "</b>", "", "<br>", "\n", "<br/>", "\n").Replace(text)
	text = html.UnescapeString(text)
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "�"))
}

func outputPages(text string) ([]string, bool) {
	truncated := len(text) > maxOutputBytes
	if truncated {
		text = text[:maxOutputBytes]
	}
	text = cleanText(text)
	// Replacing invalid UTF-8 can expand the string. Enforce the display budget
	// after normalization as well, without repeatedly rescanning malformed input.
	if len(text) > maxOutputBytes {
		truncated = true
		end := maxOutputBytes
		for !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end]
	}
	pages := []string{}
	for len(text) > pageBytes {
		end := pageBytes
		for !utf8.RuneStart(text[end]) {
			end--
		}
		pages = append(pages, text[:end])
		text = text[end:]
	}
	pages = append(pages, text)
	return pages, truncated
}

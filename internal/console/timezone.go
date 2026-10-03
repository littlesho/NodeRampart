// SPDX-License-Identifier: MIT

package console

import (
	"fmt"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/littlesho/NodeRampart/internal/timezones"
	"github.com/rivo/tview"
)

func (u *ui) editTimezone(f field) {
	u.editTimezoneWithBack(f, func() { u.configGroup(f.group) })
}

func (u *ui) editTimezoneWithBack(f field, back func()) {
	current := u.snapshot.Config.Reports.Timezone
	at := time.Now() // One reference instant for the whole selector, including searches.
	zones := timezones.List(at, current)
	search := tview.NewInputField().SetLabel(u.tr("Search: ", "搜索：")).SetFieldWidth(0)
	search.SetAcceptanceFunc(func(text string, _ rune) bool {
		if len(text) > 256 {
			return false
		}
		for _, r := range text {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return false
			}
		}
		return true
	})
	list := tview.NewList().ShowSecondaryText(true)
	var matches []timezones.Zone
	choose := func() {
		index := list.GetCurrentItem()
		if index < 0 || index >= len(matches) {
			return // Empty search is not a selection.
		}
		zone := matches[index]
		if search.GetText() == "" && u.app.GetFocus() == search && zone.Name != current {
			return // Empty search must not write the first catalog item implicitly.
		}
		if !zone.Available {
			u.output(u.tr("Timezone rules unavailable", "时区规则不可用"), u.tr("No rules could be loaded for this name. Keep the current setting or install/update timezone data; no draft change was made.", "无法加载此名称的规则。请保留当前设置，或安装/更新时区数据；草稿未修改。"), func() { u.editTimezoneWithBack(f, back) })
			return
		}
		if zone.Name != current {
			u.snapshot.Config.Reports.Timezone = zone.Name
			u.changed[f.path], u.dirty = true, true
		}
		back()
	}
	refresh := func(query string) {
		matches = timezones.Search(zones, query)
		list.Clear()
		selected := 0
		for i, zone := range matches {
			label := zone.English
			if u.lang == "zh" {
				label = zone.Chinese
			}
			if zone.Available {
				label += " · " + zone.Offset
			} else {
				label += u.tr(" · rules unavailable", " · 规则不可用")
			}
			if zone.Name == current {
				label = u.tr("Current: ", "当前配置：") + label
				selected = i
			}
			list.AddItem(tview.Escape(label), tview.Escape(zone.Name), 0, choose)
		}
		if len(matches) == 0 {
			list.AddItem(u.tr("No matching timezones", "没有匹配的时区"), u.tr("Change the search or press Esc; the draft is unchanged.", "请修改搜索或按 Esc 返回；草稿不变。"), 0, nil)
		} else {
			list.SetCurrentItem(selected)
		}
	}
	search.SetChangedFunc(refresh)
	search.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			choose()
		} else {
			u.app.SetFocus(list)
		}
	})
	search.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyDown {
			u.app.SetFocus(list)
			return nil
		}
		return event
	})
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab || event.Rune() == '/' {
			u.app.SetFocus(search)
			return nil
		}
		return event
	})
	refresh("")
	help := tview.NewTextView().SetDynamicColors(false).SetText(fmt.Sprintf("%s%s\n%s %s\n%s\n%s\n%s", u.tr("Current configuration: ", "当前配置："), cleanText(current), u.tr("Offsets at", "偏移参考时刻"), at.UTC().Format(time.RFC3339), u.tr("Current offsets may change with the date and DST.", "当前偏移可能随日期与夏令时变化。"), u.tr("Search names, cities, Chinese regions or UTC offsets.", "可搜索名称、城市、中文地域或 UTC 偏移。"), u.tr("Enter: keep in draft · Esc: cancel; aliases stay unchanged.", "Enter 暂存草稿 · Esc 取消；旧别名保持不变。")))
	flex := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(help, 6, 0, false).AddItem(search, 1, 0, true).AddItem(list, 0, 1, false)
	u.root(u.tr("Choose report timezone", "选择报告时区"), flex, back)
	u.app.SetFocus(search)
}

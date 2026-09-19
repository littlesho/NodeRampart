// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"fmt"
	"github.com/littlesho/NodeRampart/internal/config"
	"strconv"
	"strings"

	"github.com/rivo/tview"
)

var groups = []struct{ key, en, zh string }{
	{"general", "Host identity", "主机名称"}, {"sensor", "Network observation", "网络观察"},
	{"auth", "SSH authentication", "SSH 认证"}, {"detection", "Detection thresholds", "检测阈值"},
	{"notifications", "Notifications", "通知"}, {"reports", "Daily reports", "日报"},
	{"geo", "Local GeoIP paths", "本地 GeoIP 路径"}, {"privacy", "IP privacy", "IP 隐私"},
	{"billing", "Egress estimates", "出站费用估算"}, {"storage", "Storage budget", "存储预算"},
	{"budget_alerts", "Egress budget alerts", "出站预算告警"}, {"health_alerts", "Collection and storage alerts", "采集与存储告警"},
	{"paths", "File and socket paths (advanced)", "文件与套接字路径（高级）"},
}

func (u *ui) configuration() {
	if u.busy {
		u.output(u.tr("Configuration", "功能配置"), u.tr("Wait for the current operation to finish before editing configuration.", "请等待当前操作结束后再编辑配置。"), u.home)
		return
	}
	if !u.loaded {
		u.background(u.tr("Load configuration", "加载配置"), func(ctx context.Context) func() {
			snapshot, err := u.backend.Load(ctx)
			return func() {
				if err != nil {
					u.output(u.tr("Configuration unavailable", "配置不可用"), u.tr("The configuration could not be loaded. Run diagnosis or recover a previous configuration from Services.", "无法加载配置。请运行诊断，或在服务菜单恢复之前的配置。"), u.home)
					return
				}
				u.snapshot = snapshot
				u.baseline = cloneConfig(snapshot.Config)
				u.loaded = true
				u.configuration()
			}
		}, u.home)
		return
	}
	items := []menuItem{}
	for _, group := range groups {
		g := group
		items = append(items, menuItem{g.en, g.zh, "Edit settings in this group.", "编辑本组设置。", func() { u.configGroup(g.key) }})
	}
	items = append(items,
		menuItem{"Validate draft", "检查配置草稿", "Check all settings together before saving.", "保存前联合检查所有设置。", func() { u.validateDraft() }},
		menuItem{"Review and save", "确认并保存", "Save validated settings; applying may briefly restart services.", "保存有效配置；生效时可能短暂重启服务。", u.saveConfiguration},
		menuItem{"Discard edits and reload", "放弃修改并重新加载", "Reload the latest file without saving this draft.", "重新读取最新文件，不保存当前草稿。", func() {
			u.confirm(u.tr("Discard this draft and reload the current configuration?", "放弃当前草稿并重新加载配置？"), func() { u.dirty = false; u.changed = map[string]bool{}; u.loaded = false; u.configuration() }, u.configuration)
		}},
	)
	title := u.tr("Configuration", "功能配置") + fmt.Sprintf(" · schema %d", u.snapshot.Config.SchemaVersion)
	if u.dirty {
		title += u.tr(" · unsaved edits", " · 有未保存修改")
	}
	u.menu(title, title, items, u.home)
}

func (u *ui) configGroup(key string) {
	items := []menuItem{}
	title := key
	for _, g := range groups {
		if g.key == key {
			title = u.tr(g.en, g.zh)
		}
	}
	for _, definition := range fields {
		if definition.group != key {
			continue
		}
		f := definition
		value := cleanText(fieldText(&u.snapshot.Config, f.path))
		if value == "" {
			value = u.tr("(empty)", "（空）")
		}
		items = append(items, menuItem{f.en, f.zh, value, value, func() { u.editField(f) }})
	}
	u.menu(title, title, items, u.configuration)
}

func (u *ui) editField(f field) {
	form := tview.NewForm()
	value := fieldText(&u.snapshot.Config, f.path)
	read := func() string { return value }
	if len(f.choices) > 0 {
		current := 0
		for i, choice := range f.choices {
			if choice == value {
				current = i
			}
		}
		labels := append([]string(nil), f.choices...)
		if f.choices[0] == "true" {
			labels = []string{u.tr("Enabled", "启用"), u.tr("Disabled", "关闭")}
		}
		selected := current
		form.AddDropDown(u.tr(f.en, f.zh), labels, current, func(_ string, index int) { selected = index })
		read = func() string { return f.choices[selected] }
	} else {
		input := tview.NewInputField().SetLabel(u.tr(f.en, f.zh)).SetText(value).SetFieldWidth(40)
		input.SetAcceptanceFunc(func(text string, _ rune) bool { return len(text) <= 4096 })
		form.AddFormItem(input)
		read = input.GetText
	}
	back := func() { u.configGroup(f.group) }
	form.AddButton(u.tr("Keep in draft", "暂存到草稿"), func() {
		candidate := u.snapshot.Config
		candidate.Sensor.Interfaces = append([]string(nil), candidate.Sensor.Interfaces...)
		if err := setField(&candidate, f.path, read()); err != nil {
			u.output(u.tr("Invalid value", "输入无效"), u.tr("The value has an invalid type, unsupported characters, or exceeds the size limit. See the field's format and limits.", "输入类型无效、包含不支持的字符或超过长度限制。请参考字段格式与范围。"), func() { u.editField(f) })
			return
		}
		u.snapshot.Config = candidate
		u.changed[f.path] = true
		u.dirty = true
		back()
	})
	form.AddButton(u.tr("Cancel", "取消"), back)
	help := tview.NewTextView().SetDynamicColors(false).SetText(u.tr(f.helpEN, f.helpZH) + "\n" + f.path)
	flex := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(help, 5, 0, false).AddItem(form, 0, 1, true)
	u.root(u.tr("Edit setting", "编辑设置"), flex, back)
}

func (u *ui) validateDraft() {
	message := u.tr("Configuration is valid. Save to apply it.", "配置有效；保存后生效。")
	if err := u.snapshot.Config.Validate(); err != nil {
		message = err.Error()
	}
	u.output(u.tr("Draft validation", "草稿检查"), message, u.configuration)
}

func (u *ui) saveConfiguration() {
	if !u.dirty {
		u.output(u.tr("Configuration", "功能配置"), u.tr("There are no unsaved changes.", "没有未保存的修改。"), u.configuration)
		return
	}
	if err := u.snapshot.Config.Validate(); err != nil {
		u.output(u.tr("Fix settings before saving", "请先修正配置"), err.Error(), u.configuration)
		return
	}
	diff := configDiff(u.baseline, u.snapshot.Config)
	if diff == "" {
		u.dirty = false
		u.changed = map[string]bool{}
		u.output(u.tr("Configuration", "功能配置"), u.tr("There are no effective changes.", "没有实际修改。"), u.configuration)
		return
	}
	pages, truncated := outputPages(diff)
	u.outputPageAction(u.tr("Review configuration changes", "检查配置修改"), pages, 0, truncated, u.tr("Save and apply", "保存并应用"), func() {
		u.confirm(u.tr("Save and apply these settings? Running services may briefly restart.", "保存并应用这些设置？正在运行的服务可能短暂重启。"), func() {
			snapshot := u.snapshot
			snapshot.Config.Sensor.Interfaces = append([]string(nil), snapshot.Config.Sensor.Interfaces...)
			u.background(u.tr("Save configuration", "保存配置"), func(ctx context.Context) func() {
				text, err := u.backend.Save(ctx, snapshot)
				var refreshed Snapshot
				var loadErr error
				if err == nil {
					refreshed, loadErr = u.backend.Load(ctx)
				}
				return func() {
					if err != nil {
						if text != "" {
							text = err.Error() + "\n\n" + text
						} else {
							text = err.Error()
						}
						u.output(u.tr("Configuration save did not complete", "配置保存未完成"), cleanText(text), u.configuration)
						return
					}
					u.dirty = false
					u.changed = map[string]bool{}
					u.loaded = loadErr == nil
					if loadErr == nil {
						u.snapshot = refreshed
						u.baseline = cloneConfig(refreshed.Config)
					} else {
						text += "\n" + u.tr("Saved, but the refreshed configuration could not be read. Reload before editing again.", "已保存，但无法重新读取配置；再次编辑前请重新加载。")
					}
					u.output(u.tr("Configuration saved", "配置已保存"), text, u.configuration)
				}
			}, u.configuration)
		}, u.configuration)
	}, u.configuration)
}

func cloneConfig(c config.Config) config.Config {
	c.Sensor.Interfaces = append([]string(nil), c.Sensor.Interfaces...)
	return c
}

// Only configured values are compared. Secret files are never opened by the UI.
func configDiff(before, after config.Config) string {
	var lines []string
	for _, f := range fields {
		old, next := fieldText(&before, f.path), fieldText(&after, f.path)
		if old == next {
			continue
		}
		lines = append(lines, f.path+"\n  "+strconv.Quote(old)+" → "+strconv.Quote(next))
	}
	return strings.Join(lines, "\n\n")
}

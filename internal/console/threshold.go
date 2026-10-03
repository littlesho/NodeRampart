// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"encoding/json"

	"github.com/rivo/tview"
)

func (u *ui) previewThresholdDraft() {
	u.previewThresholdDraftWithBack(u.configuration)
}

func (u *ui) previewThresholdDraftWithBack(back func()) {
	if err := u.snapshot.Config.Validate(); err != nil {
		u.output(u.tr("Fix draft first", "请先修正草稿"), err.Error(), back)
		return
	}
	input := tview.NewInputField().SetLabel(u.tr("Offline metadata file", "离线元数据文件")).SetFieldWidth(50)
	input.SetAcceptanceFunc(func(text string, _ rune) bool { return len(text) <= 4096 })
	form := tview.NewForm().AddFormItem(input)
	form.AddButton(u.tr("Preview", "试运行"), func() {
		current, _ := json.Marshal(u.baseline)
		draft, _ := json.Marshal(u.snapshot.Config)
		arguments := map[string]string{"input": input.GetText(), "current_json": string(current), "draft_json": string(draft)}
		u.background(u.tr("Offline threshold preview", "离线阈值试运行"), func(ctx context.Context) func() {
			text, err := u.backend.Action(ctx, "threshold_preview_draft", arguments)
			return func() {
				if err != nil {
					u.output(u.tr("Preview unavailable", "无法试运行"), err.Error(), back)
					return
				}
				text = u.tr("Fewer alerts do not prove fewer false positives. Other draft settings are not exercised. Preview does not save configuration.\n\n", "告警减少不证明误报率下降；其他草稿设置未参与验证。试运行不会保存配置。\n\n") + text
				pages, truncated := outputPages(text)
				u.outputPageAction(u.tr("Threshold preview", "阈值试运行"), pages, 0, truncated, u.tr("Review and save draft", "检查并保存草稿"), func() { u.saveConfigurationWithBack(back) }, back)
			}
		}, back)
	})
	form.AddButton(u.tr("Cancel", "取消"), back)
	u.root(u.tr("Preview draft using local offline metadata", "使用本地离线元数据试运行草稿"), form, back)
}

// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/rivo/tview"
)

func (u *ui) priceProvider(back func()) {
	u.menu("Choose a provider for reports", "选择报告使用的云厂商", []menuItem{
		{"AWS", "AWS", "Fetch current regional public Internet egress tariffs.", "获取区域当前公开公网出站价格。", func() { u.priceForm("aws", back) }},
		{"OCI", "OCI", "Choose one of the three official geographic groups.", "选择三个官方地理分组之一。", func() { u.priceForm("oci", back) }},
	}, back)
}

func (u *ui) priceForm(provider string, back func()) {
	a, _ := findAction("prices_fetch")
	a.helpEN += " Fetching also activates this provider's profile for daily reports. Cached estimates for both providers remain viewable."
	a.helpZH += " 获取成功后将此厂商配置用于日报；仍可查看两家已缓存的估算。"
	a.params = append([]parameter(nil), a.params...)
	a.params[0] = choice("provider", "Selected provider", "所选厂商", provider)
	if provider == "oci" {
		a.params[1] = choice("region", "OCI geographic group", "OCI 地理分组", "north-america-europe-uk", "asia-pacific-japan-south-america", "middle-east-africa")
	}
	if provider == "aws" {
		u.background(u.tr("Load AWS regions", "加载 AWS 区域"), func(ctx context.Context) func() {
			raw, err := u.backend.Action(ctx, "prices_regions", map[string]string{})
			var data struct {
				AWS []string `json:"aws"`
			}
			valid := err == nil && len(raw) <= maxOutputBytes && json.Unmarshal([]byte(raw), &data) == nil && len(data.AWS) > 0 && len(data.AWS) <= 256
			if valid {
				for _, region := range data.AWS {
					if len(region) == 0 || len(region) > 64 {
						valid = false
						break
					}
				}
			}
			return func() {
				if !valid {
					u.menu("AWS region list unavailable", "AWS 区域列表不可用", []menuItem{{"Retry region list", "重试区域列表", "Fetch the public AWS region catalog.", "获取 AWS 公开区域目录。", func() { u.priceForm(provider, back) }}, {"Enter a region manually", "手工填写区域", "Use a known AWS pricing region.", "使用已知的 AWS 价格区域。", func() { u.actionForm(a, func() { u.priceProvider(back) }) }}}, func() { u.priceProvider(back) })
					return
				}
				a.params[1] = choice("region", "AWS region", "AWS 区域", data.AWS...)
				u.actionForm(a, func() { u.priceProvider(back) })
			}
		}, func() { u.priceProvider(back) })
		return
	}
	u.actionForm(a, func() { u.priceProvider(back) })
}

// The ordinary custom-profile flow edits typed fields and individual tiers.
// The separate JSON import remains available for experienced operators.
func (u *ui) newCustomProfile() {
	if u.dirty {
		u.output(u.tr("Unsaved configuration", "配置尚未保存"), u.tr("Save or discard the configuration draft before applying a tariff.", "应用价格前请先保存或放弃配置草稿。"), u.configuration)
		return
	}
	profile := billing.Profile{SchemaVersion: 1, Name: "Custom Internet egress", Provider: "custom", SourceRegion: "custom", Currency: "USD", EffectiveDate: time.Now().Format(time.DateOnly), UnitBytes: 1 << 30, InternetEgress: []billing.Tier{{UpToGB: 0, PricePerGB: 0}}}
	u.customProfile(&profile)
}

func (u *ui) customProfile(profile *billing.Profile) {
	items := []menuItem{}
	for _, definition := range []struct{ key, en, zh string }{
		{"name", "Profile name", "配置名称"}, {"provider", "Provider name", "厂商名称"}, {"source_region", "Source region", "来源区域"}, {"currency", "Currency (three uppercase letters)", "货币（三个大写字母）"}, {"effective_date", "Tariff effective date (YYYY-MM-DD)", "价格生效日期（YYYY-MM-DD）"}, {"source_url", "Official source URL (HTTPS)", "官方来源 URL（HTTPS）"}, {"free_gb", "Monthly free allowance assigned to this host", "分配给本机本月的免费额度"}, {"unit_bytes", "Bytes per tariff GB", "每价格 GB 的字节数"},
	} {
		d := definition
		items = append(items, menuItem{d.en, d.zh, profileField(profile, d.key), profileField(profile, d.key), func() { u.editProfileField(profile, d.key, d.en, d.zh) }})
	}
	items = append(items,
		menuItem{"Price tiers", "阶梯价格", "Edit cumulative paid-unit boundaries; last tier is unbounded.", "编辑付费单位的累计上界；最后一档无上界。", func() { u.profileTiers(profile) }},
		menuItem{"Validate and save profile", "检查并保存费用配置", "Activate this profile for egress estimates and reports.", "将此配置用于出站费用估算与日报。", func() {
			if err := profile.Validate(); err != nil {
				u.output(u.tr("Invalid profile", "费用配置无效"), err.Error(), func() { u.customProfile(profile) })
				return
			}
			data, err := json.Marshal(profile)
			if err != nil {
				return
			}
			a, _ := findAction("billing_profile_save")
			u.prepareAction(a, map[string]string{"profile_json": string(data)}, u.prices)
		}},
	)
	u.menu("Custom egress tariff draft", "自定义出站价格草稿", items, func() {
		u.confirm(u.tr("Discard this custom tariff draft?", "放弃此自定义价格草稿？"), u.prices, func() { u.customProfile(profile) })
	})
}

func profileField(p *billing.Profile, key string) string {
	switch key {
	case "name":
		return p.Name
	case "provider":
		return p.Provider
	case "source_region":
		return p.SourceRegion
	case "currency":
		return p.Currency
	case "effective_date":
		return p.EffectiveDate
	case "source_url":
		return p.SourceURL
	case "free_gb":
		return strconv.FormatFloat(p.FreeGB, 'g', -1, 64)
	case "unit_bytes":
		return strconv.FormatUint(p.UnitBytes, 10)
	}
	return ""
}

func setProfileField(profile *billing.Profile, key, value string) bool {
	if len(value) > 4096 {
		return false
	}
	switch key {
	case "name":
		profile.Name = value
	case "provider":
		profile.Provider = value
	case "source_region":
		profile.SourceRegion = value
	case "currency":
		profile.Currency = value
	case "effective_date":
		profile.EffectiveDate = value
	case "source_url":
		profile.SourceURL = value
	case "free_gb":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
		profile.FreeGB = v
	case "unit_bytes":
		v, err := strconv.ParseUint(value, 10, 64)
		if err != nil || (v != 1<<30 && v != 1_000_000_000) {
			return false
		}
		profile.UnitBytes = v
	default:
		return false
	}
	return true
}

func (u *ui) editProfileField(profile *billing.Profile, key, en, zh string) {
	form := tview.NewForm()
	input := tview.NewInputField().SetLabel(u.tr(en, zh)).SetText(profileField(profile, key)).SetFieldWidth(40)
	input.SetAcceptanceFunc(func(text string, _ rune) bool { return len(text) <= 4096 })
	form.AddFormItem(input)
	back := func() { u.customProfile(profile) }
	form.AddButton(u.tr("Keep in draft", "暂存到草稿"), func() {
		if !setProfileField(profile, key, input.GetText()) {
			u.output(u.tr("Invalid value", "输入无效"), u.tr("Use a valid field value; the byte unit must be 1073741824 or 1000000000.", "请输入有效值；字节单位须为 1073741824 或 1000000000。"), func() { u.editProfileField(profile, key, en, zh) })
			return
		}
		back()
	})
	form.AddButton(u.tr("Cancel", "取消"), back)
	u.root(u.tr("Custom tariff setting", "自定义价格设置"), form, back)
}

func (u *ui) profileTiers(profile *billing.Profile) {
	items := []menuItem{}
	for index, tier := range profile.InternetEgress {
		i := index
		label := fmt.Sprintf("%d · %.8g / GB · up to %.8g", index+1, tier.PricePerGB, tier.UpToGB)
		items = append(items, menuItem{label, label, "0 boundary means unbounded; only the last tier may be unbounded.", "上界 0 表示无上界；仅最后一档可无上界。", func() { u.editTier(profile, i) }})
	}
	if len(profile.InternetEgress) < 32 {
		items = append(items, menuItem{"Add a tier", "添加价格档位", "At most 32 tiers. Check all cumulative boundaries before saving.", "最多 32 档；保存前检查所有累计上界。", func() {
			u.editTier(profile, len(profile.InternetEgress))
		}})
	}
	u.menu("Price tiers", "阶梯价格", items, func() { u.customProfile(profile) })
}

func (u *ui) editTier(profile *billing.Profile, index int) {
	adding := index == len(profile.InternetEgress)
	if index < 0 || index > len(profile.InternetEgress) || adding && index >= 32 {
		return
	}
	var tier billing.Tier
	if !adding {
		tier = profile.InternetEgress[index]
	}
	form := tview.NewForm()
	boundary := tview.NewInputField().SetLabel(u.tr("Paid-unit upper boundary (0 = unlimited)", "付费单位上界（0 表示无限）")).SetText(strconv.FormatFloat(tier.UpToGB, 'g', -1, 64)).SetFieldWidth(20)
	price := tview.NewInputField().SetLabel(u.tr("Price per tariff GB", "每价格 GB 单价")).SetText(strconv.FormatFloat(tier.PricePerGB, 'g', -1, 64)).SetFieldWidth(20)
	for _, input := range []*tview.InputField{boundary, price} {
		input.SetAcceptanceFunc(func(s string, _ rune) bool { return len(s) <= 64 })
		form.AddFormItem(input)
	}
	back := func() { u.profileTiers(profile) }
	form.AddButton(u.tr("Keep in draft", "暂存到草稿"), func() {
		limit, e1 := strconv.ParseFloat(boundary.GetText(), 64)
		rate, e2 := strconv.ParseFloat(price.GetText(), 64)
		if e1 != nil || e2 != nil || limit < 0 || rate < 0 || rate > billing.MaxPricePerGB || math.IsNaN(limit) || math.IsNaN(rate) || math.IsInf(limit, 0) || math.IsInf(rate, 0) {
			u.output(u.tr("Invalid tier", "价格档位无效"), u.tr("Enter finite, non-negative values; the rate must not exceed 1e100.", "请输入有限、非负的数值；单价不能超过 1e100。"), func() { u.editTier(profile, index) })
			return
		}
		tier := billing.Tier{UpToGB: limit, PricePerGB: rate}
		if adding {
			profile.InternetEgress = append(profile.InternetEgress, tier)
		} else {
			profile.InternetEgress[index] = tier
		}
		back()
	})
	if !adding && len(profile.InternetEgress) > 1 {
		form.AddButton(u.tr("Delete tier", "删除档位"), func() {
			profile.InternetEgress = append(profile.InternetEgress[:index], profile.InternetEgress[index+1:]...)
			back()
		})
	}
	form.AddButton(u.tr("Cancel", "取消"), back)
	u.root(u.tr("Edit tariff tier", "编辑价格档位"), form, back)
}

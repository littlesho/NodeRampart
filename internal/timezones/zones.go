// SPDX-License-Identifier: MIT

// Package timezones supplies a reviewable offline selector directory. It does
// not change timezone names, scheduling, or civil-day storage semantics.
package timezones

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
)

//go:embed names.txt
var directory string

type Zone struct {
	Name, English, Chinese, Offset string
	Common, Available              bool
}

// Offset uses the zone active at the supplied instant, including DST and
// fractional-hour offsets. Historical second offsets are retained as well.
func Offset(at time.Time) string {
	_, seconds := at.Zone()
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	result := fmt.Sprintf("UTC%s%02d:%02d", sign, seconds/3600, seconds/60%60)
	if seconds%60 != 0 {
		result += fmt.Sprintf(":%02d", seconds%60)
	}
	return result
}

var common = map[string][2]string{
	"Local": {"System local timezone", "系统本地时区"}, "UTC": {"Coordinated Universal Time", "协调世界时"},
	"Asia/Shanghai": {"China · Beijing / Shanghai", "中国 · 北京 / 上海"}, "Asia/Hong_Kong": {"Hong Kong", "香港"},
	"Asia/Taipei": {"Taipei", "台北"}, "Asia/Tokyo": {"Japan · Tokyo", "日本 · 东京"},
	"Asia/Seoul": {"South Korea · Seoul", "韩国 · 首尔"}, "Asia/Singapore": {"Singapore", "新加坡"},
	"Asia/Kolkata": {"India · Kolkata", "印度 · 加尔各答"}, "Asia/Kathmandu": {"Nepal · Kathmandu", "尼泊尔 · 加德满都"},
	"Asia/Dubai": {"United Arab Emirates · Dubai", "阿联酋 · 迪拜"}, "Asia/Bangkok": {"Thailand · Bangkok", "泰国 · 曼谷"},
	"Europe/London": {"United Kingdom · London", "英国 · 伦敦"}, "Europe/Paris": {"France · Paris", "法国 · 巴黎"},
	"Europe/Berlin": {"Germany · Berlin", "德国 · 柏林"}, "Europe/Moscow": {"Russia · Moscow", "俄罗斯 · 莫斯科"},
	"America/New_York": {"United States · New York", "美国 · 纽约"}, "America/Chicago": {"United States · Chicago", "美国 · 芝加哥"},
	"America/Denver": {"United States · Denver", "美国 · 丹佛"}, "America/Los_Angeles": {"United States · Los Angeles", "美国 · 洛杉矶"},
	"America/Toronto": {"Canada · Toronto", "加拿大 · 多伦多"}, "America/Vancouver": {"Canada · Vancouver", "加拿大 · 温哥华"},
	"America/Sao_Paulo": {"Brazil · São Paulo", "巴西 · 圣保罗"}, "Africa/Johannesburg": {"South Africa · Johannesburg", "南非 · 约翰内斯堡"},
	"Africa/Cairo": {"Egypt · Cairo", "埃及 · 开罗"}, "Australia/Sydney": {"Australia · Sydney", "澳大利亚 · 悉尼"},
	"Australia/Adelaide": {"Australia · Adelaide", "澳大利亚 · 阿德莱德"}, "Australia/Perth": {"Australia · Perth", "澳大利亚 · 珀斯"},
	"Pacific/Auckland": {"New Zealand · Auckland", "新西兰 · 奥克兰"}, "Pacific/Chatham": {"New Zealand · Chatham", "新西兰 · 查塔姆"},
}

// List offers path-qualified IANA names plus Local/UTC for new selections and
// freezes every displayed offset at one instant. Keep the configured name
// exactly, including an old top-level abbreviation or host-specific entry.
// Top-level abbreviations are only present when they are the current value. Missing rules
// remain visible but unavailable: absence must not silently select another zone.
func List(at time.Time, current string) []Zone {
	return list(at, current, time.LoadLocation)
}

// Translate only the stable geographic prefix. Names without an explicit
// common translation retain readable original proper names, without guessing.
func readableNames(name string) (english, chinese string) {
	parts := strings.Split(strings.ReplaceAll(name, "_", " "), "/")
	english = strings.Join(parts, " · ")
	regions := map[string]string{
		"Africa": "非洲", "America": "美洲", "Asia": "亚洲", "Europe": "欧洲",
		"Australia": "澳大利亚", "Pacific": "太平洋", "Indian": "印度洋",
		"Atlantic": "大西洋", "Antarctica": "南极洲", "Arctic": "北极", "Etc": "其它",
		"US": "美国", "Canada": "加拿大", "Brazil": "巴西", "Chile": "智利", "Mexico": "墨西哥",
	}
	if region, ok := regions[parts[0]]; ok {
		parts[0] = region
	}
	return english, strings.Join(parts, " · ")
}

func rank(z Zone) int {
	if z.Name == "Local" {
		return 0
	}
	if z.Name == "UTC" {
		return 1
	}
	if z.Common {
		return 2
	}
	return 3
}

func list(at time.Time, current string, load func(string) (*time.Location, error)) []Zone {
	names := map[string]bool{"Local": true, "UTC": true}
	for _, line := range strings.Split(directory, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") && strings.Contains(line, "/") {
			names[line] = true
		}
	}
	if current != "" {
		names[current] = true
	}
	zones := make([]Zone, 0, len(names))
	for name := range names {
		english, chinese := readableNames(name)
		z := Zone{Name: name, English: english, Chinese: chinese}
		if label, ok := common[name]; ok {
			z.English, z.Chinese, z.Common = label[0], label[1], true
		}
		if loc, err := load(name); err == nil {
			z.Available, z.Offset = true, Offset(at.In(loc))
		}
		zones = append(zones, z)
	}
	sort.Slice(zones, func(i, j int) bool {
		if rank(zones[i]) != rank(zones[j]) {
			return rank(zones[i]) < rank(zones[j])
		}
		return zones[i].Name < zones[j].Name
	})
	return zones
}

// Search matches the original name, spaced city name, bilingual common label,
// and displayed offset. The bounded catalog is filtered without reloading rules.
func Search(zones []Zone, query string) []Zone {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return append([]Zone(nil), zones...)
	}
	result := []Zone{}
	for _, z := range zones {
		_, regional := readableNames(z.Name)
		text := z.Name + " " + strings.ReplaceAll(z.Name, "_", " ") + " " + z.English + " " + z.Chinese + " " + regional + " " + z.Offset
		if strings.Contains(strings.ToLower(text), query) {
			result = append(result, z)
		}
	}
	return result
}

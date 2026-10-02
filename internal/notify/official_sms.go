// SPDX-License-Identifier: MIT

package notify

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const gsmBasic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
const gsmExtension = "\f^{}\\[~]|€"

func officialUTF16Units(body string) int {
	units := 0
	for _, r := range body {
		units++
		if r > 0xffff {
			units++
		}
	}
	return units
}

// EstimateSMS counts GSM-7 septets (extension characters cost two), or UTF-16
// code units. Multipart capacities are deliberately conservative at 152/66.
func EstimateSMS(body string) (string, int, error) {
	if !safeOfficialText(body, 1800) {
		return "", 0, errors.New("SMS text is invalid or exceeds its bound")
	}
	septets, units, gsm := 0, 0, true
	for _, r := range body {
		units++
		if r > 0xffff {
			units++
		}
		switch {
		case strings.ContainsRune(gsmBasic, r):
			septets++
		case strings.ContainsRune(gsmExtension, r):
			septets += 2
		default:
			gsm = false
		}
	}
	if gsm {
		if septets <= 160 {
			return "gsm7", 1, nil
		}
		return "gsm7", (septets + 151) / 152, nil
	}
	if units <= 70 {
		return "ucs2", 1, nil
	}
	return "ucs2", (units + 65) / 66, nil
}

// RenderOfficialSMS preserves severity, time, core summary, coverage, local
// command, product identity and unsubscribe instructions. Only the host label
// may shorten, visibly. An overlong essential summary is rejected before debit.
func RenderOfficialSMS(s OfficialSemantic, language string, maxSegments int) (string, string, int, error) {
	if language != "en" && language != "zh" || maxSegments < 1 || maxSegments > 2 {
		return "", "", 0, errors.New("SMS language or segment policy is invalid")
	}
	footer := "Reply STOP to opt out."
	if language == "zh" {
		footer = "回复 STOP 退订。"
	}
	host := s.HostAlias
	for {
		body := "NodeRampart " + s.Severity + " " + s.Time + "\n" + host + " " + s.EventKind + " " + s.Phase + "\n" + s.BoundedSummary + "\n" + s.Coverage + "\n" + s.LocalReference + "\n" + footer
		encoding, segments, err := EstimateSMS(body)
		if err != nil {
			return "", "", 0, err
		}
		if segments <= maxSegments {
			return body, encoding, segments, nil
		}
		if host == "~" {
			return "", "", 0, errors.New("SMS essential summary exceeds the allowed segment count")
		}
		host = strings.TrimSuffix(host, "~")
		_, size := utf8.DecodeLastRuneInString(host)
		if size <= 0 {
			host = "~"
		} else {
			host = host[:len(host)-size] + "~"
		}
	}
}

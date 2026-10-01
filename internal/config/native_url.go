// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// NativeCredential is stored only in a protected file, never in configuration.
type NativeCredential struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

var (
	nativeSegment   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	nativeSnowflake = regexp.MustCompile(`^[1-9][0-9]{5,24}$`)
	feishuPath      = regexp.MustCompile(`^/open-apis/bot/v2/hook/[A-Za-z0-9_-]{16,128}$`)
	slackPath       = regexp.MustCompile(`^/services/T[A-Z0-9]{7,32}/B[A-Z0-9]{7,32}/[A-Za-z0-9]{16,256}$`)
	googleChatPath  = regexp.MustCompile(`^/v1/spaces/[A-Za-z0-9_-]{1,128}/messages$`)
	teamsHost       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}(?:\.[a-z0-9]{2})?\.environment\.api\.powerplatform\.com$`)
	teamsLogicHost  = regexp.MustCompile(`^prod-[0-9]{1,3}\.[a-z0-9]{2,32}\.logic\.azure\.com$`)
	teamsPath       = regexp.MustCompile(`^/powerautomate/automations/direct/(?:cu/[0-9]{1,6}/)?workflows/[A-Za-z0-9_-]{16,128}/triggers/manual/paths/invoke$`)
	teamsLogicPath  = regexp.MustCompile(`^/workflows/[A-Za-z0-9_-]{16,128}/triggers/manual/paths/invoke$`)
)

func ValidateNativeCredential(channel string, credential NativeCredential) error {
	if _, err := NativeHTTPSURL(channel, credential.URL); err != nil {
		return err
	}
	if credential.Secret != "" && (channel != "feishu" || len(credential.Secret) > 256 || strings.IndexFunc(credential.Secret, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0) {
		return errors.New("native signing secret is invalid or unsupported for this channel")
	}
	return nil
}

// NativeHTTPSURL accepts only the vendor's selected incoming webhook contract.
// Errors deliberately never include the URL or its protected query values.
func NativeHTTPSURL(channel, value string) (*url.URL, error) {
	invalid := errors.New("native webhook requires a supported official HTTPS URL and query contract")
	if len(value) == 0 || len(value) > 4096 || strings.IndexFunc(value, func(r rune) bool { return r <= 0x20 || r >= 0x7f }) >= 0 || strings.Contains(value, "\\") {
		return nil, invalid
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.ForceQuery || u.RawPath != "" || (u.Port() != "" && u.Port() != "443") || u.Hostname() != strings.ToLower(u.Hostname()) || strings.Contains(u.Hostname(), "%") {
		return nil, invalid
	}
	// Path escapes are not needed for any supported vendor endpoint. Reject
	// even a canonical-looking escaped slash to avoid differing interpretations.
	if strings.Contains(u.EscapedPath(), "%") {
		return nil, invalid
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, invalid
	}
	for k, v := range q {
		if len(v) != 1 || v[0] == "" || strings.IndexFunc(k+v[0], func(r rune) bool { return r <= 0x20 || r >= 0x7f }) >= 0 {
			return nil, invalid
		}
	}
	for _, pair := range strings.Split(u.RawQuery, "&") {
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || strings.Contains(parts[0], "%") || (parts[0] != "sp" && strings.Contains(parts[1], "%")) {
			return nil, invalid
		}
	}
	host := u.Hostname()
	ok := false
	switch channel {
	case "feishu":
		ok = host == "open.feishu.cn" && feishuPath.MatchString(u.Path) && len(q) == 0
	case "wecom":
		ok = host == "qyapi.weixin.qq.com" && u.Path == "/cgi-bin/webhook/send" && len(q) == 1 && nativeSegment.MatchString(q.Get("key")) && len(q.Get("key")) >= 16
	case "discord":
		parts := strings.Split(u.Path, "/")
		if len(parts) == 6 && parts[1] == "api" && parts[2] == "v10" {
			parts = append(parts[:2], parts[3:]...)
		}
		ok = host == "discord.com" && len(parts) == 5 && parts[1] == "api" && parts[2] == "webhooks" && nativeSnowflake.MatchString(parts[3]) && nativeSegment.MatchString(parts[4]) && len(parts[4]) >= 20 && (len(q) == 0 || (len(q) == 1 && q.Get("wait") == "true"))
		if ok {
			q.Set("wait", "true")
			u.Path = "/api/v10/webhooks/" + parts[3] + "/" + parts[4]
		}
	case "slack":
		ok = host == "hooks.slack.com" && slackPath.MatchString(u.Path) && len(q) == 0
	case "teams":
		ok = ((teamsHost.MatchString(host) && teamsPath.MatchString(u.Path)) || (teamsLogicHost.MatchString(host) && teamsLogicPath.MatchString(u.Path))) && len(q) == 4 && (q.Get("api-version") == "1" || q.Get("api-version") == "2016-06-01") && q.Get("sp") == "/triggers/manual/run" && q.Get("sv") == "1.0" && nativeSegment.MatchString(q.Get("sig")) && len(q.Get("sig")) >= 20
	case "google_chat":
		ok = host == "chat.googleapis.com" && googleChatPath.MatchString(u.Path) && len(q) == 2 && nativeSegment.MatchString(q.Get("key")) && len(q.Get("key")) >= 20 && nativeSegment.MatchString(q.Get("token")) && len(q.Get("token")) >= 20
	}
	if !ok {
		return nil, invalid
	}
	u.RawQuery = q.Encode()
	return u, nil
}

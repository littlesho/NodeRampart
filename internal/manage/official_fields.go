// SPDX-License-Identifier: MIT

package manage

import (
	"errors"
	"strings"

	"github.com/littlesho/NodeRampart/internal/config"
)

func (m *Manager) updatedOfficialCredential(channel, path string, input map[string]string) (config.OfficialCredential, error) {
	invalid := errors.New("credential fields are invalid; blank retains a field, explicit clear removes it / 凭据字段无效；留空保留原字段，明确清空才移除")
	var c config.OfficialCredential
	fields := config.OfficialCredentialFieldNames(channel)
	if input["credential_json"] != "" {
		for _, field := range append(fields, "clear_fields") {
			if input[field] != "" {
				return c, invalid
			}
		}
		return config.DecodeOfficialCredential(channel, []byte(input["credential_json"]))
	}
	if existingNativeCredential(path) {
		var err error
		c, err = m.readOfficialCredential(channel, path)
		if err != nil {
			return c, err
		}
	}
	c.Channel = channel
	clear := map[string]bool{}
	if input["clear_fields"] != "" {
		for _, key := range strings.Split(input["clear_fields"], ",") {
			known := false
			for _, field := range fields {
				if field == key {
					known = true
				}
			}
			if !known || clear[key] || input[key] != "" {
				return config.OfficialCredential{}, invalid
			}
			clear[key] = true
		}
	}
	changed := len(clear) > 0
	pointers := map[string]*string{"app_id": &c.AppID, "app_secret": &c.AppSecret, "target_type": &c.TargetType, "target_id": &c.TargetID, "channel_access_token": &c.ChannelAccessToken, "account_sid": &c.AccountSID, "auth_mode": &c.AuthMode, "api_key_sid": &c.APIKeySID, "api_key_secret": &c.APIKeySecret, "auth_token": &c.AuthToken, "from": &c.From, "messaging_service_sid": &c.MessagingServiceSID, "to": &c.To, "phone_number_id": &c.PhoneNumberID, "access_token": &c.AccessToken, "recipient": &c.Recipient, "graph_version": &c.GraphVersion}
	for _, key := range fields {
		if dst := pointers[key]; dst != nil {
			if clear[key] {
				*dst = ""
			} else if input[key] != "" {
				*dst = input[key]
				changed = true
			}
		}
	}
	if channel == "whatsapp_cloud" {
		if c.Templates == nil {
			c.Templates = &config.OfficialTemplates{}
		}
		for kind, dst := range map[string]**config.OfficialTemplate{"event": &c.Templates.Event, "daily": &c.Templates.Daily, "test": &c.Templates.Test} {
			for _, suffix := range []string{"name", "language", "parameters"} {
				key := kind + "_" + suffix
				if input[key] == "" && !clear[key] {
					continue
				}
				changed = true
				if *dst == nil {
					*dst = &config.OfficialTemplate{Parameters: []string{}}
				}
				t := *dst
				switch suffix {
				case "name":
					t.Name = input[key]
				case "language":
					t.Language = input[key]
				case "parameters":
					if clear[key] {
						t.Parameters = []string{}
					} else {
						t.Parameters = strings.Split(input[key], ",")
					}
				}
			}
			if *dst != nil && (*dst).Name == "" && (*dst).Language == "" && len((*dst).Parameters) == 0 {
				*dst = nil
			}
		}
	}
	if !changed {
		return config.OfficialCredential{}, errors.New("replacement requires changed fields; use keep to retain the snapshot / 替换须填写修改字段；保留原快照请选择保留")
	}
	if err := config.ValidateOfficialCredential(channel, c); err != nil {
		return config.OfficialCredential{}, invalid
	}
	return c, nil
}

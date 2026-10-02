// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"path/filepath"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/notify"
)

func validateOfficialReferences(cfg config.Config, directory string) error {
	for _, channel := range config.OfficialChannelNames() {
		setting := cfg.Notifications.OfficialChannels()[channel]
		if !setting.Enabled {
			continue
		}
		parent := filepath.Dir(setting.CredentialFile)
		if parent != directory && parent != filepath.Join(directory, "secrets") {
			return errors.New("official protected credential reference is outside the configuration directory / 官方渠道受保护凭据引用不在配置目录内")
		}
		if _, err := notify.NewOfficial(channel, setting); err != nil {
			return errors.New("official protected credentials or template policy unavailable / 官方渠道受保护凭据或模板策略不可用")
		}
	}
	return nil
}

// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/littlesho/NodeRampart/internal/config"
	"golang.org/x/sys/unix"
)

func secretReferences(c config.Config) []string {
	paths := []string{c.Notifications.Telegram.TokenFile, c.Notifications.Webhook.CredentialFile, c.Heartbeat.CredentialFile, c.Privacy.HashKeyFile}
	for _, name := range config.NativeChannelNames() {
		paths = append(paths, c.Notifications.NativeChannels()[name].CredentialFile)
	}
	return paths
}

func (m *Manager) readNativeCredential(channel, path string) (config.NativeCredential, error) {
	var credential config.NativeCredential
	base := filepath.Dir(m.ConfigPath)
	if filepath.Dir(path) != base && filepath.Dir(path) != filepath.Join(base, "secrets") {
		return credential, errors.New("native credentials must stay in the managed configuration directory / 原生凭据须位于托管配置目录")
	}
	f, stat, err := openManagedFile(path, 8192, false, m.daemonUID)
	if err != nil {
		return credential, errors.New("native webhook credentials are missing or unsafe / 原生 Webhook 凭据缺失或不安全")
	}
	defer f.Close()
	mode := stat.Mode & 0o777
	if (stat.Uid != uint32(m.daemonUID) && stat.Uid != 0) || (mode != 0o600 && !(mode == 0o640 && stat.Uid == 0 && stat.Gid == uint32(m.daemonGID))) || m.daemonReadable(path, false) != nil {
		return credential, errors.New("native webhook credentials require daemon-readable protected permissions / 原生 Webhook 凭据须受保护且服务身份可读")
	}
	// Recheck the pinned file after reading; a replacement path cannot alter
	// the credential snapshot being validated by this management operation.
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	var after unix.Stat_t
	if err != nil || len(data) > 8192 || unix.Fstat(int(f.Fd()), &after) != nil || after.Size != stat.Size || after.Mtim != stat.Mtim || after.Ctim != stat.Ctim || after.Mode != stat.Mode || after.Uid != stat.Uid || after.Gid != stat.Gid || after.Nlink != stat.Nlink || after.Dev != stat.Dev || after.Ino != stat.Ino {
		return credential, errors.New("native webhook credentials changed or exceeded their limit / 原生 Webhook 凭据发生变化或超限")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, decodeErr := decoder.Token()
	if decodeErr != nil || start != json.Delim('{') {
		return credential, errors.New("native webhook credential document is invalid / 原生 Webhook 凭据文档无效")
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, keyErr := decoder.Token()
		name, ok := key.(string)
		if keyErr != nil || !ok || seen[name] || name != "url" && name != "secret" {
			return credential, errors.New("native webhook credential fields are invalid / 原生 Webhook 凭据字段无效")
		}
		value, valueErr := decoder.Token()
		text, isString := value.(string)
		if valueErr != nil || !isString {
			return credential, errors.New("native webhook credential fields must be strings / 原生 Webhook 凭据字段须为字符串")
		}
		seen[name] = true
		if name == "url" {
			credential.URL = text
		} else {
			credential.Secret = text
		}
	}
	end, endErr := decoder.Token()
	if !seen["url"] || endErr != nil || end != json.Delim('}') || decoder.Decode(new(any)) != io.EOF {
		return credential, errors.New("native webhook credential document is invalid / 原生 Webhook 凭据文档无效")
	}
	if err := config.ValidateNativeCredential(channel, credential); err != nil {
		return credential, errors.New("native webhook URL or signing secret is invalid / 原生 Webhook URL 或签名密钥无效")
	}
	return credential, nil
}

// native stages a new immutable credential file before the existing journaled
// config transaction. Failed applies retain referenced recovery files; no save
// operation contacts a webhook or implicitly queues a test notification.
func (m *Manager) native(ctx context.Context, channel string, input map[string]string) (string, error) {
	if !config.IsNativeChannel(channel) {
		return "", errors.New("unsupported native channel / 不支持的原生渠道")
	}
	snapshot, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	n := snapshot.Config.Notifications.NativeChannels()[channel]
	if input["enabled"] != "yes" && input["enabled"] != "no" {
		return "", errors.New("choose enable or disable / 请选择启用或停用")
	}
	n.Enabled = input["enabled"] == "yes"
	if language, present := input["language"]; present {
		if language != "en" && language != "zh" {
			return "", errors.New("message language must be en or zh / 消息语言必须为 en 或 zh")
		}
		n.Language = language
	}
	mode := input["credential_action"]
	if mode == "" {
		mode = "keep"
	}
	if mode != "keep" && mode != "replace" && mode != "clear" {
		return "", errors.New("choose keep, replace or clear credentials / 请选择保留、替换或清空凭据")
	}
	signMode := input["secret_action"]
	if signMode == "" {
		signMode = "keep"
	}
	if signMode != "keep" && signMode != "replace" && signMode != "clear" || channel != "feishu" && (signMode != "keep" || input["secret"] != "") {
		return "", errors.New("only Feishu supports an optional signing secret / 仅飞书支持可选签名密钥")
	}
	if mode != "replace" && input["url"] != "" || signMode != "replace" && input["secret"] != "" {
		return "", errors.New("select replace before entering a credential / 输入新凭据前请选择替换")
	}
	if mode == "clear" {
		if n.Enabled || signMode != "keep" {
			return "", errors.New("disable this channel before clearing credentials / 清空凭据前请停用本渠道")
		}
		n.CredentialFile = ""
	}
	created := ""
	if mode == "replace" || mode == "keep" && signMode != "keep" {
		credential := config.NativeCredential{}
		// Keep the signing secret only if an existing file is actually present;
		// the first URL configuration may omit the optional Feishu signature.
		if mode == "keep" || channel == "feishu" && signMode == "keep" {
			old, readErr := m.readNativeCredential(channel, n.CredentialFile)
			if readErr != nil && (mode == "keep" || existingNativeCredential(n.CredentialFile)) {
				return "", readErr
			}
			if readErr == nil {
				credential = old
			}
		}
		if mode == "replace" {
			credential.URL = input["url"]
		}
		if signMode == "replace" {
			if input["secret"] == "" {
				return "", errors.New("replacement signing secret cannot be blank; choose clear / 替换签名密钥不可留空；移除请选清空")
			}
			credential.Secret = input["secret"]
		} else if signMode == "clear" {
			credential.Secret = ""
		}
		if err := config.ValidateNativeCredential(channel, credential); err != nil {
			return "", errors.New("native webhook URL or signing secret is invalid / 原生 Webhook URL 或签名密钥无效")
		}
		data, err := json.Marshal(credential)
		if err != nil {
			return "", errors.New("credentials could not be encoded / 凭据无法编码")
		}
		created, err = m.newSecret(channel, append(data, '\n'))
		if err != nil {
			return "", err
		}
		n.CredentialFile = created
	}
	if err := snapshot.Config.Notifications.SetNativeChannel(channel, n); err != nil {
		return "", err
	}
	result, err := m.saveLocked(ctx, snapshot)
	if err != nil && created != "" {
		m.removeUnreferencedSecret(created)
	}
	if err == nil && (created != "" || mode == "clear") {
		keep := secretReferences(snapshot.Config)
		dir := filepath.Join(filepath.Dir(m.ConfigPath), "secrets")
		_, statErr := os.Lstat(dir)
		if !errors.Is(statErr, os.ErrNotExist) {
			if cleanupErr := m.cleanManagedFiles(dir, channel+"-", ".secret", keep...); cleanupErr != nil {
				return result, errors.New("settings saved; old managed credential cleanup needs inspection / 设置已保存；旧托管凭据清理需检查")
			}
		}
	}
	if err == nil {
		result += "\n" + strings.ToUpper(channel) + " settings saved without sending a test. Changed URL or signing credentials isolate old pending bodies; disabled unchanged targets pause delivery. Old messages retain their language and report timezone. In-flight requests may finish at their original target.\n设置已保存，未发送测试消息。修改 URL 或签名凭据会隔离旧积压；停用未变更目标仅暂停投递。旧消息保留原语言与报告时区。已发送中的请求可能在原目标完成。"
	}
	return result, err
}

func existingNativeCredential(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, os.ErrNotExist)
}

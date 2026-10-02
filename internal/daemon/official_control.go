// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/store"
)

var reconciliationReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func (a *App) officialTestMessage(channel string, preview bool) (store.OutboxMessage, notify.Sender, string, error) {
	c := a.options.Config.Notifications.OfficialChannels()[channel]
	sender := a.options.OfficialNotifiers[channel]
	if sender == nil && preview {
		allowed := a.options.ConfigDirectory != "" && (filepath.Dir(c.CredentialFile) == a.options.ConfigDirectory || filepath.Dir(c.CredentialFile) == filepath.Join(a.options.ConfigDirectory, "secrets"))
		if !allowed {
			return store.OutboxMessage{}, nil, "", errors.New("protected credential reference unavailable")
		}
		candidate, err := notify.NewOfficial(channel, c)
		if err != nil {
			return store.OutboxMessage{}, nil, "", errors.New("protected official credentials unavailable or unsafe")
		}
		sender = candidate
	}
	identified, ok := sender.(interface{ Destination() string })
	if !ok {
		return store.OutboxMessage{}, nil, "", errors.New("selected official sender unavailable")
	}
	language := config.OfficialChannelLanguage(c)
	body, semantic := notify.FormatOfficialTest(a.options.Config.Hostname, language)
	m := store.OutboxMessage{ID: model.NewID("msg"), DedupeKey: "test:" + model.NewID("once"), Channel: channel, Destination: identified.Destination(), PrivacyMode: a.options.Config.Privacy.NotificationIP, Language: language, Timezone: a.report.Location.String(), Body: body, LogicalKind: "test"}
	if err := prepareOfficialMessage(sender, &m, semantic); err != nil {
		return m, sender, "", errors.New("subscription, template or message limits do not permit this test")
	}
	policy, _ := json.Marshal(c)
	sum := sha256.Sum256([]byte(m.Destination + "\x00" + m.Body + "\x00" + m.FrozenPayload + "\x00" + string(policy)))
	return m, sender, hex.EncodeToString(sum[:]), nil
}

func (a *App) officialControl(ctx context.Context, command string, args api.NotifyChannelArgs, now time.Time) api.Response {
	if command != "notify_test" && command != "notify_preview" {
		return controlFailure("invalid official notification command")
	}
	if args.ConfirmPaid && !config.IsPaidOfficialChannel(args.Channel) || args.PreviewID != "" && !config.IsPaidOfficialChannel(args.Channel) || command == "notify_preview" && (args.ConfirmPaid || args.PreviewID != "") {
		return controlFailure("invalid paid test confirmation")
	}
	m, sender, previewID, err := a.officialTestMessage(args.Channel, command == "notify_preview")
	if err != nil {
		return controlFailure("selected official notification test requires valid protected credentials, subscription and message limits / 所选官方通知测试须有有效受保护凭据、订阅与消息限额")
	}
	c := a.options.Config.Notifications.OfficialChannels()[args.Channel]
	if command == "notify_preview" {
		target := "protected target"
		if p, ok := sender.(interface{ RecipientPreview() string }); ok {
			target = p.RecipientPreview()
		}
		budget, err := a.options.Store.OfficialChannelStatus(ctx, args.Channel, now)
		if err != nil {
			return controlFailure("local notification budget unavailable; no network request was made / 本地通知额度不可用；未发出网络请求")
		}
		nextReset := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
		return api.Response{Version: api.Version, OK: true, Data: map[string]any{"channel": args.Channel, "body": m.Body, "language": m.Language, "target": target, "frozen_request": json.RawMessage(m.FrozenPayload), "estimated_segments": m.EstimatedSegments, "encoding": m.Encoding, "daily_message_limit": c.DailyMessageLimit, "daily_segment_limit": c.DailySegmentLimit, "budget": budget, "next_reset_utc": nextReset.Format(time.RFC3339), "cost": "unknown", "preview_id": previewID, "network_sent": false, "requires_paid_confirmation": config.IsPaidOfficialChannel(args.Channel)}}
	}
	if !c.Enabled || !c.Subscription.Allows("test") {
		return controlFailure("selected official sender is disabled or test subscription is revoked / 所选官方渠道已停用或测试订阅已撤销")
	}
	if config.IsPaidOfficialChannel(args.Channel) && (!args.ConfirmPaid || args.PreviewID != previewID) {
		return controlFailure("paid test requires confirmation of the current local preview; no message was queued / 付费测试须确认当前本地预览；未入队发送")
	}
	if _, err := a.options.Store.Enqueue(ctx, m); err != nil {
		return controlFailure("could not queue selected official test / 无法将所选官方测试入队")
	}
	return api.Response{Version: api.Version, OK: true, Data: map[string]string{"status": "queued", "id": m.ID, "channel": args.Channel}}
}

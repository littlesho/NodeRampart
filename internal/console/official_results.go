// SPDX-License-Identifier: MIT

package console

var officialDispatchLabels = map[string][2]string{
	"api_accepted":     {"Provider accepted the request; delivery is not confirmed", "平台已接受请求；尚未确认送达"},
	"reserved":         {"Durable intent reserved; no remote confirmation", "已持久化投递意图；尚无远端确认"},
	"in_flight":        {"Request in flight; remote outcome unconfirmed", "请求正在发送；远端结果未确认"},
	"retry_ready":      {"Eligible for a bounded retry", "可进行有界重试"},
	"accepted":         {"Provider accepted the request; delivery is not confirmed", "平台已接受请求；尚未确认送达"},
	"delivery_unknown": {"Remote outcome unknown; automatic resending held", "远端结果未知；已暂停自动重发"},
	"blocked":          {"Delivery held by a local safety condition", "投递因本地安全条件暂停"},
	"queued":           {"Provider queued the SMS", "平台已将 SMS 排队"},
	"sending":          {"Provider is sending the SMS", "平台正在发送 SMS"},
	"sent":             {"Upstream carrier accepted the SMS", "上游运营商已接受 SMS"},
	"delivered":        {"Provider reports delivery; this does not prove reading", "平台报告已送达；不代表已读"},
	"undelivered":      {"Provider reports the SMS undelivered", "平台报告 SMS 未送达"},
	"failed":           {"Provider reports failure", "平台报告失败"},
	"canceled":         {"Provider reports the SMS cancelled", "平台报告 SMS 已取消"},
	"poll_unconfirmed": {"SMS status observation is unconfirmed", "SMS 状态观察未获确认"},
	"unknown":          {"Provider delivery status remains unknown", "平台投递状态仍未知"},
}

func officialUnknownObservation(key, language string) (string, bool) {
	switch key {
	case "platform_segments", "platform_price", "platform_charges":
		return notificationText([2]string{"Unknown", "未知"}, language), true
	default:
		return "", false
	}
}

// The same stored state has different observability for a Teams workflow and
// an official provider. Use the sibling channel instead of changing old labels.
func officialRowStateLabel(row map[string]any, key, value, language string) (string, bool) {
	channel, _ := row["channel"].(string)
	if _, official := officialBrands[channel]; !official || key != "state" {
		return "", false
	}
	label, ok := officialDispatchLabels[value]
	return notificationText(label, language), ok
}

var officialNotificationTexts = map[string][2]string{
	"paid_notification_restore_hold":                                                           {"Paid notification restore hold", "付费通知恢复暂停"},
	"notification_recipient_opted_out":                                                         {"Recipient opt-out persists", "接收者退订持续生效"},
	"notification_delivery_unknown":                                                            {"Remote notification outcome is unknown", "远端通知结果未知"},
	"Paid delivery is held after restoring a historical ledger.":                               {"Paid delivery is held after restoring a historical ledger.", "恢复历史账本后付费投递已暂停。"},
	"Reconcile external receipts and budgets explicitly before allowing new submissions.":      {"Reconcile external receipts and budgets explicitly before allowing new submissions.", "允许新提交前须明确核对外部回执与额度。"},
	"A recipient opt-out persists; ordinary resume or credential rotation cannot clear it.":    {"A recipient opt-out persists; ordinary resume or credential rotation cannot clear it.", "接收者退订持续生效；普通恢复或凭据轮换不能解除。"},
	"Obtain a new consent basis and follow the provider's permitted recovery process.":         {"Obtain a new consent basis and follow the provider's permitted recovery process.", "取得新的同意依据并遵守平台允许的恢复流程。"},
	"Some dispatch intents may have been accepted externally; automatic resubmission is held.": {"Some dispatch intents may have been accepted externally; automatic resubmission is held.", "部分发送意图可能已在外部受理；已暂停自动重发。"},
	"Inspect the local notification records and external receipts; do not silently retry.":     {"Inspect the local notification records and external receipts; do not silently retry.", "检查本地通知记录与外部回执；不能静默重试。"},
	"official_render_rejected":                                                                 {"Notification could not be rendered within the safe platform boundary", "通知无法在安全平台边界内呈现"},
	"rate_limited":                                                                             {"Provider rate limit; obey the persisted cooldown", "平台限流；须遵守已持久化冷却"},
	"request_rejected":                                                                         {"Provider rejected the request", "平台已拒绝请求"},
	"unconfirmed_response":                                                                     {"Provider response does not confirm acceptance", "平台响应未确认接受请求"},
	"monthly_quota_exhausted":                                                                  {"Platform monthly quota is exhausted", "平台月度额度已耗尽"},
	"api_accepted":                                                                             {"Interface accepted the request; delivery remains unconfirmed", "接口已接受请求；送达仍未确认"},
	"payload_invalid":                                                                          {"Notification request payload is invalid", "通知请求正文无效"},
	"retry_window_expired":                                                                     {"Safe retry window expired; automatic resend held", "安全重试窗口已到期；已暂停自动重发"},
	"clock_rollback":                                                                           {"Clock moved backwards; sending held", "时钟回退；已暂停发送"},
	"durable_intent_required":                                                                  {"Sending requires a durable reserved intent", "发送须有持久化预留意图"},
	"frozen_request_invalid":                                                                   {"Frozen request or target no longer matches", "冻结请求或目标已不匹配"},
	"message_expired":                                                                          {"Notification expired; it was not sent", "通知已到期；未发送"},
	"poll_intent_invalid":                                                                      {"SMS status observation intent is invalid", "SMS 状态观察意图无效"},
	"unsupported_channel":                                                                      {"This notification channel is unsupported", "不支持此通知渠道"},
	"provider_rejected":                                                                        {"Platform rejected the request; inspect channel setup", "平台拒绝请求；请检查渠道设置"},
	"recipient_opted_out":                                                                      {"Recipient withdrew consent; sending is locked", "接收者撤回同意；发送已锁定"},
	"token_unavailable":                                                                        {"Protected platform access token is unavailable", "受保护平台访问 Token 不可用"},
	"unconfirmed_provider_failure":                                                             {"Platform failure does not establish whether the request was accepted", "平台故障未能确定请求是否已接受"},
	"token_rejected":                                                                           {"Platform rejected token authentication", "平台拒绝 Token 鉴权"},
	"token_request_invalid":                                                                    {"Platform token request is invalid", "平台 Token 请求无效"},
	"token_response_invalid":                                                                   {"Platform token response is invalid", "平台 Token 响应无效"},
	"token_wait_canceled":                                                                      {"Waiting for the platform token was cancelled", "等待平台 Token 已取消"},
	"carrier_accepted":                                                                         {"Upstream carrier accepted the SMS; delivery is unconfirmed", "上游运营商已接受 SMS；送达未确认"},
	"carrier_delivered":                                                                        {"Platform reports SMS delivery; this does not prove reading", "平台报告 SMS 已送达；不代表已读"},
	"delivery_failed":                                                                          {"Platform reports notification delivery failure", "平台报告通知投递失败"},
	"poll_response_invalid":                                                                    {"SMS status observation response is invalid", "SMS 状态观察响应无效"},
	"poll_receipt_invalid":                                                                     {"SMS status receipt does not match the protected target", "SMS 状态回执与受保护目标不匹配"},
	"poll_request_invalid":                                                                     {"SMS status observation request is invalid", "SMS 状态观察请求无效"},
	"credential_or_permission_rejected":                                                        {"Platform rejected credentials or permissions", "平台拒绝凭据或权限"},
	"quality_or_policy_limited":                                                                {"Platform quality or policy restriction requires attention", "平台质量或策略限制需要处理"},
	"template_unavailable":                                                                     {"Approved template is unavailable or its mapping is invalid", "已批准模板不可用或映射无效"},
	"official_attempt_limit":                                                                   {"Bounded dispatch attempt limit reached", "已达到有界发送尝试上限"},
	"official_budget_day_changed":                                                              {"Queued message belongs to an earlier budget day", "队列消息属于较早额度日期"},
	"official_clock_moved_backwards":                                                           {"Clock moved backwards; notification delivery held", "时钟回退；已暂停通知投递"},
	"official_daily_budget_exhausted":                                                          {"Daily notification budget exhausted", "每日通知额度已耗尽"},
	"official_delivery_error":                                                                  {"Official notification delivery failed", "官方通知投递失败"},
	"official_delivery_unknown":                                                                {"Remote outcome unknown; inspect external receipts before new paid attempts", "远端结果未知；新的付费尝试前请核对外部回执"},
	"official_interrupted_dispatch":                                                            {"Interrupted request has an uncertain remote result", "请求被中断；远端结果不确定"},
	"official_poll_deadline":                                                                   {"SMS status observation deadline reached; outcome remains unknown", "SMS 状态观察期限已到；结果仍未知"},
	"official_poll_limit":                                                                      {"Bounded SMS status observation limit reached", "已达到有界 SMS 状态观察上限"},
	"official_restore_reconciliation_required":                                                 {"Paid notifications need external receipt and budget reconciliation after restore", "恢复后付费通知须核对外部回执与额度"},
	"official_restored_body_requires_reconciliation":                                           {"Restored pending body remains held; it is not resent", "恢复的未发正文继续保留；不会重发"},
	"official_segment_limit":                                                                   {"SMS exceeds the configured bounded segment limit", "SMS 超过所配置的有界分段上限"},
	"official_subscription_opted_out":                                                          {"Recipient opted out; normal resume and token rotation cannot clear this lock", "接收者已退订；普通恢复与 Token 轮换不能解除此锁"},
	"official_subscription_paused":                                                             {"Notification subscription is paused", "通知订阅已暂停"},
	"official_subscription_required":                                                           {"This notification type requires active recipient consent", "此通知类型须获得有效接收者同意"},
	"official_subscription_revoked":                                                            {"Recipient consent revoked; pending notifications suppressed", "接收者同意已撤销；未发通知已抑制"},
}

func init() {
	labels := map[string][2]string{"official_channels": {"Official notification channels", "官方通知渠道"}, "dispatch_state": {"Persistent dispatch state", "持久化发送状态"}, "provider_state": {"Observed provider state", "已观察平台状态"}, "logical_kind": {"Notification purpose type", "通知用途类型"}, "estimated_segments": {"Estimated SMS segments", "估算 SMS 分段"}, "encoding": {"SMS encoding", "SMS 编码"}, "poll_attempts": {"Bounded SMS status checks", "有界 SMS 状态检查次数"}, "provider_id": {"Correlatable provider receipt ID", "可关联的平台回执 ID"}, "accepted_at_utc": {"Provider acceptance time (UTC)", "平台接受时间（UTC）"}, "daily_message_limit": {"Daily logical-message budget", "每日逻辑消息额度"}, "daily_segment_limit": {"Daily estimated SMS segment budget", "每日估算 SMS 分段额度"}, "max_segments": {"Maximum SMS segments per message", "每条 SMS 最大分段数"}, "logical_messages": {"Reserved logical messages", "已预留逻辑消息数"}, "estimated_segments_reserved": {"Reserved estimated SMS segments", "已预留估算 SMS 分段数"}, "restore_hold": {"Paid restore reconciliation hold", "付费恢复核对暂停"}, "optout": {"Recipient opt-out lock", "接收者退订锁"}}
	for key, label := range labels {
		resultLabels[key] = label
	}
	for key, label := range map[string][2]string{
		"first_attempt_at_utc":            {"First dispatch intent time (UTC)", "首次发送意图时间（UTC）"},
		"next_reset_utc":                  {"Next local UTC-day reset", "下次本地 UTC 日重置时间"},
		"platform_segments":               {"Observed platform SMS segments", "已观察平台 SMS 分段"},
		"platform_price":                  {"Observed platform price (unknown when unavailable)", "已观察平台费用（不可用时未知）"},
		"provider_delivery_status":        {"Observed provider delivery status", "已观察平台投递状态"},
		"price_unit":                      {"Observed platform currency", "已观察平台币种"},
		"http_status":                     {"Observed HTTP status", "已观察 HTTP 状态码"},
		"api_error_code":                  {"Observed API error code", "已观察 API 错误码"},
		"official_policies":               {"Official channel subscription and UTC-day limits", "官方渠道订阅与 UTC 日限额"},
		"revoked":                         {"Recipient consent revoked", "接收者同意已撤销"},
		"opted_out":                       {"Recipient opt-out lock", "接收者退订锁"},
		"restore_reconciliation_required": {"Paid restore reconciliation required", "须进行付费恢复核对"},
		"consented_at_utc":                {"Recipient consent recorded at (UTC)", "接收者同意记录时间（UTC）"},
		"clock_highwater_utc":             {"Persisted latest clock time (UTC)", "已持久化最新时钟时间（UTC）"},
		"logical_messages_reserved":       {"Logical messages reserved in this UTC day", "本 UTC 日已预留逻辑消息数"},
		"delivery_unknown":                {"Requests with unknown remote outcome", "远端结果未知的请求数"},
		"pruned_budget_days":              {"Expired local budget days removed", "已移除的到期本地额度日期数"},
		"platform_charges":                {"Actual platform charges (unknown when unavailable)", "实际平台费用（不可用时未知）"},
	} {
		resultLabels[key] = label
	}
}

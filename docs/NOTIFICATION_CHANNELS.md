# Native Webhook notification channels

<!-- current-release:start -->
[简体中文](NOTIFICATION_CHANNELS.zh-CN.md). This guide targets the current published
[v0.4.0-alpha.11](https://github.com/littlesho/NodeRampart/releases/tag/v0.4.0-alpha.11).
<!-- current-release:end -->

These six native channels first shipped in alpha.8 and share the notification
menu with the six other channels. See the [current Release/Latest validation scope](RELEASE_VERIFICATION.md#alpha11-ordinary-release-and-latest-promotion),
historical [feature acceptance](ALPHA8_ACCEPTANCE.md), [privacy](PRIVACY.md) and
[user agreement](USER_AGREEMENT.md).

Real vendor API tests and human receipt remain **NOT RUN**. Product maturity
remains Alpha.

## Common operation

All official interface and terms references in this guide were checked on 2026-10-01.

One independent target is supported for each of Feishu, WeCom, Discord, Slack, Teams and Google Chat. All default disabled, can run together with Telegram and the unchanged generic JSON Webhook, and receive eligible SSH/network/budget/health events, start/update/recovery notices and the shared daily report schedule. Existing severity/silence rules still apply. This is outbound HTTPS only: no inbound bot, command handler, private-message discovery, file upload, OAuth installation, listener or extra runtime.

Run `sudo noderampart tui` and open **Notification channels**. Select the channel's setup, enter the complete vendor URL in the hidden input, choose `en` or `zh` independently of the terminal language, and review/apply. Blank unchanged inputs retain secrets; use the explicit URL/signing actions to replace or clear. Saving, opening settings and status never sends a test. Enable only after your organization permits this target. Use **Send a test notification**, select exactly one channel, and inspect the receiver and the local queue. The CLI equivalent queues only a synthetic test:

```sh
sudo noderampart notify test --channel feishu
sudo noderampart notify status
sudo noderampart notify list --limit 20
```

Replace `feishu` with `wecom`, `discord`, `slack`, `teams` or `google_chat` for a separate explicit test. A queued result is local admission. Sent means the selected interface acknowledged its defined contract, never that anyone read it. Teams only confirms workflow request acceptance. Use event timelines for independent outcomes when some targets fail.

Ordinary configuration contains references, never full URLs or signing secrets. Minimal reference-only example (merge with your existing configuration rather than replacing it):

```json
{
  "notifications": {
    "feishu": {"enabled": false, "credential_file": "/etc/noderampart/feishu.credential.json", "language": "en", "timeout": "10s"},
    "wecom": {"enabled": false, "credential_file": "/etc/noderampart/wecom.credential.json", "language": "en", "timeout": "10s"},
    "discord": {"enabled": false, "credential_file": "/etc/noderampart/discord.credential.json", "language": "en", "timeout": "10s"},
    "slack": {"enabled": false, "credential_file": "/etc/noderampart/slack.credential.json", "language": "en", "timeout": "10s"},
    "teams": {"enabled": false, "credential_file": "/etc/noderampart/teams.credential.json", "language": "en", "timeout": "10s"},
    "google_chat": {"enabled": false, "credential_file": "/etc/noderampart/google_chat.credential.json", "language": "en", "timeout": "10s"}
  }
}
```

The manager creates versioned owned files in its protected secrets directory. Each file is strict JSON with `url` and optional Feishu `secret`; do not put it in Git, CLI flags, screenshots or support evidence. Managed files use the daemon's existing service ownership and minimal read permissions. Manual references are allowed only alongside the actual configuration file or
its `secrets` subdirectory, and must be regular, single-link files in trusted real directories; no symlinks, shared-writable parents or world access. Supported modes are `0600` for the owning daemon/root and `0640` only for the verified service group. A root-only unreadable file makes that channel unavailable. Configuration dump/API and review show references, not contents. Keep credentials separately from the database backup.

The native transports use direct validated DNS/IP connections, verified TLS on port 443, no redirects and no system proxy. Correct DNS/routing is required; TLS interception is not enabled. Legacy generic Webhook/heartbeat retain their existing administrator/system proxy contract: that proxy must be trusted with bearer credentials; local IP checks do not constrain proxy-side DNS. Vendor query credentials remain in the protected request URL only.

One frozen message is at most 1,800 UTF-8 bytes before platform encoding. Request JSON is capped at 16 KiB, response headers at 16 KiB, bodies at 64 KiB, and timeout at 1–30 seconds. Messages prioritize severity/time/core metrics/coverage limitations, visibly shorten optional detail and include working local event/report commands. No report/log/diagnostic attachment, automatic splitting or retry-time translation. English and Simplified Chinese share recorded facts and the report timezone; new language/timezone settings leave old bodies and daily archive boundaries unchanged.

## Feishu

Prerequisite: membership/permission to manage a group and its custom robot, plus administrator-approved robot security settings. In the group's robot settings add **Custom Bot**, obtain its Webhook from [official setup](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot), and optionally enable signing. This is a group robot, not an application OAuth bot. Set a required keyword such as the fixed product name `NodeRampart` if your group requires it; configure any IP allowlist for the legitimate egress. Do not bypass group policy.

Choose Feishu setup and enter the URL and optional signing secret privately. Text requests use `msg_type: text` and `content.text`. Signing uses Unix **seconds**, HMAC-SHA256 with key `timestamp + newline + secret` and an empty message, then Base64; it is regenerated for each attempt. This differs from DingTalk. Success requires HTTP 200 and numeric `code: 0`; missing/wrong fields fail. Business `11232` is rate limiting. Correct security/signature/keyword errors locally before retrying. Official bounds checked 2026-10-01: 20 KB request, 100 requests/minute and 5/second; NodeRampart is more conservative.

Disable to pause; replace credentials through the hidden setup to isolate old unsent work. Revoke/remove the custom bot in Feishu when retiring the integration.

## WeCom

Prerequisite: a permitted WeCom group with authority to add a group robot. Use **group settings → group robot → add** and copy its URL from [official group robot documentation](https://developer.work.weixin.qq.com/document/path/91770). This is neither an intelligent-bot connection nor a CorpSecret application. The `key` query is secret; there is no invented signing option.

Choose WeCom setup, save/review and explicitly test. Requests use `msgtype: text`, `text.content` and empty mention lists. HTTP 200 must contain numeric `errcode: 0`; business `45009` produces durable rate limiting, `-1` is transient, and other business failures are quarantined. Check the robot/key and group permissions for permanent failure; do not expose the response body. Checked 2026-10-01: text ≤2,048 UTF-8 bytes, 20 messages/minute; successful deliveries persist a minimum three-second hold. Disable locally and revoke the robot key in WeCom for retirement.

## Discord

Prerequisite: a server/channel administrator grants **Manage Webhooks** for the intended channel. Create an incoming webhook under **channel settings → integrations → webhooks** using [Execute Webhook documentation](https://docs.discord.com/developers/resources/webhook). Store its complete secret URL through Discord setup; this permits only its bound channel, not arbitrary users.

NodeRampart executes with `wait=true`; success requires HTTP 200 with valid message and channel IDs. It disables all allowed mentions, neutralizes mention syntax and places dynamic text in a safe code block. No response URL is followed. Official content limit is 2,000 characters; rate limits depend on the route/bucket. HTTP 429 and fractional JSON `retry_after` are honored along with Retry-After, without shortening excessive waits. Invalid target/permission or payload responses require local correction. Disable then rotate/delete the webhook in Discord; uncertain rotation isolates older messages.

## Slack

Prerequisite: your workspace administrator permits your own Slack App and its Incoming Webhooks feature. Follow [official creation](https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/), add an incoming webhook to the permitted workspace and select its installation channel. Enter the URL through Slack setup. No bot token, `chat.postMessage`, OAuth store installation or channel/username/avatar override is implemented.

Requests contain a fixed accessibility fallback and a `plain_text` section, avoiding event-supplied formatting and mentions. Success is exactly HTTP 200 with text `ok`. Archived channel, administrator prohibition, invalid payload/token, removed service and other documented permanent responses are quarantined; 429 persists Retry-After. Check app/channel/admin state rather than retrying revoked credentials. Slack documents roughly one message/second per channel with limited bursts; this client persists at least one second after success and does not promise burst capacity. The 1,800-byte semantic bound fits the section's 3,000-character limit. Disable locally, then revoke/recreate the webhook in the App's settings as required.

Source licensing does not grant platform distribution permission. The [API terms](https://slack.com/terms-of-service/api) require separate authorization for commercial distribution, including paid/freemium connected products. Third-party availability requires an accessible user agreement and privacy policy; these are linked above. A user-provided authorized URL does not waive those obligations. This free source project makes no Marketplace approval, partner or certification claim. A distributor must obtain any required Slack/Salesforce agreement before commercial integration distribution. NodeRampart neither accepts agreements nor registers apps for you. Checked 2026-10-01 against terms effective 2025-10-10 and [developer policy](https://docs.slack.dev/developer-policy/).

## Microsoft Teams Workflows

Prerequisite: an administrator permits Workflows/Power Automate, the connection may post to the selected Teams channel, and tenant policy permits the secret-URL **Anyone** trigger mode. Organization/Entra/OAuth-only triggers are **unsupported**; do not disable or bypass tenant requirements. No old Office 365 Connector, MessageCard or arbitrary Azure host is supported.

The selected contract is the official **When a Teams webhook request is received** trigger, configured from scratch with **Anyone**, followed by a **For each** attachment and **Post adaptive card in a chat or channel** action to a fixed channel. The action's card input is the current attachment's `content`; choose the permitted connection/team/channel. Save, verify that this simple workflow returns HTTP 202 on trigger acceptance, and use its generated callback URL in Teams setup. This is the Adaptive Card variant of **Post to a channel when a webhook request is received**, not the text-only *Send webhook alerts to a channel* variant. Follow [official creation and trigger guide](https://learn.microsoft.com/en-us/microsoftteams/platform/webhooks-and-connectors/how-to/add-incoming-webhook) and [Teams connector contract](https://learn.microsoft.com/en-us/connectors/teams/).

Complete synthetic body matching this workflow:

```json
{
  "type": "message",
  "attachments": [{
    "contentType": "application/vnd.microsoft.card.adaptive",
    "contentUrl": null,
    "content": {
      "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
      "type": "AdaptiveCard",
      "version": "1.2",
      "body": [{"type": "TextBlock", "text": "NodeRampart synthetic notification", "wrap": true}]
    }
  }]
}
```

NodeRampart supports the bounded `<environment>[.<two-character scale>].environment.api.powerplatform.com` callback hosts, paths under `/powerautomate/automations/direct/` (including documented `cu/<number>/`), and generated `prod-<number>.<region>.logic.azure.com` workflow callbacks. Required `api-version`, `sp`, `sv` and `sig` parameters are preserved. Legacy Logic runtime callbacks are used only for this Workflows trigger, never old Connector URLs. Unsupported generated URL forms fail closed; report the non-secret host/path shape without sending its query. Microsoft is migrating callback URLs; re-copy the current workflow URL when required. Do not send it to support.

Only HTTP 202 with an empty body confirms **workflow request accepted**. It cannot confirm downstream action execution, Teams display or reading. Inspect workflow run history and the Teams channel after the explicit test. Owners must keep the Teams connection authorized; add a permitted co-owner and follow organization lifecycle rules to avoid orphaned flows when an owner leaves. Repair/reauthorize failed connections and rotate the callback through the native setup. Service/tenant quotas depend on the workflow license and trigger; 429/Retry-After are honored. Historical Connector's rate/size limits are not asserted for Workflows; this client uses its own smaller resource bounds. Checked 2026-10-01; real tenant acceptance remains NOT RUN.

## Google Chat

Prerequisite: a Google Workspace administrator allows incoming webhooks in the intended space; its manager/member has permission to create one. Open **space → Apps & integrations → Webhooks**, create a NodeRampart-named webhook and copy its complete URL following [official quickstart](https://developers.google.com/workspace/chat/quickstart/webhooks). This path sends to that space only. It provides no arbitrary-account direct messaging or chat-history reading; do not request history scopes.

Choose Google Chat setup and preserve both `key` and `token` query parameters privately. JSON contains `text` with safe Markdown escaping. HTTP 200 must return a message `name` and thread name within the configured space; empty/malformed replies fail. Credential/permission/invalid target errors are permanent; 429/5xx are retried with durable cooldown. Chat message bounds are 32,000 bytes, and webhooks share a one-request/second per-space quota; multiple webhooks/apps may consume that quota. This client uses a smaller bound and at least a one-second hold. Disable locally, then delete/replace the space webhook to revoke access.

## Recovery, limits and rollback

Configuration changes use draft → validation → non-secret review → apply with the existing journal/rollback. Each sender holds the inspected credential snapshot. URL/signing credential replacement creates a new opaque identity and isolates old unsent messages. A → B → A never revives isolated A bodies. A known same-target disable pauses without clearing; resume respects leases, the seven-day TTL and durable cooldown. Privacy tightening or incomparable privacy changes isolate old bodies. Language changes affect new work only. An in-flight request may complete at the original target and cannot be recalled.

Explicit **Discard isolated notification bodies** (or `notify discard-isolated --channel slack`) erases only that selected channel's isolated unsent bodies, retaining history and cooldown. New targets do not receive historic queued events; daily notification cutover is bound to activation. Existing report backfill remains local archive only and never sends messages. Excessive/unrepresentable server waits suspend until operator resume; they are never shortened. Quarantine is for permanent credential/permission/payload failures. Retry timeouts or remote success with failed local acknowledgment may produce duplicates; use the local message/event ID to correlate, not an exactly-once promise.

The shared outbox retains 10,000 pending records/32 MiB and each of the eight original channels has an admission share of 1,250 records/4 MiB across credential rotations. Existing oversized shares are preserved and can drain; new admission waits below the share. Counts/rejections/isolation/expiry are visible in status; new native events are not coalesced with Telegram HTML. Workers are fixed at one per configured channel, ≤20 rows per pass, no network within database transactions.

The schema-13 migration introduced per-channel event decision constraints and native daily activation boundaries. The current release uses schema 14 with additional official-account state; configuration/control API remain schema 1 and sensor protocol stays 5. Back up compatible database/config/credentials before upgrading. Old binaries reject newer schemas. An intentional rollback to alpha.7 requires its matching database, configuration and credential backups restored offline; current-release rollback likewise requires the chosen older version's matching backups. Never lower an in-place schema. Uninstall preserves credentials; explicit purge is restricted to fixed product-owned paths and must not follow arbitrary references.

Manual `*.credential.json` or files such as `secrets/slack-user.secret` are not product-created credentials; purge refuses them until you move them out. Atomically replace manual credentials and restart; managed hidden setup rotation is preferred. Attempt pacing, including failures, is three seconds for WeCom and one second for other native channels. Explicit operator resume can clear a cooldown; ordinary restart/re-enable cannot. Discord requests use API v10 and a NodeRampart-owned identified User-Agent.

Back up configuration, database and credentials before native package purge. The managed removal command checks unknown credentials before removing the package. Direct `dpkg --purge` can remove package-owned conffiles before its post-removal guard rejects an unknown credential, so a failed purge does not promise an unchanged installation. The unknown credential itself is preserved.

[Alpha.9 account-based QQ Bot / LINE Push / Twilio SMS / WhatsApp templates](OFFICIAL_NOTIFICATION_CHANNELS.md) are included in the current published release with separate consent, billing and intent semantics; these six webhook contracts remain unchanged.

# Official account notification channels — alpha.9

[简体中文](OFFICIAL_NOTIFICATION_CHANNELS.zh-CN.md) · [First six native channels](NOTIFICATION_CHANNELS.md) · [Privacy](PRIVACY.md) · [User agreement](USER_AGREEMENT.md)

Alpha.9 is an **Unreleased development candidate**. It adds `qqbot`, `line`,
`twilio_sms` and `whatsapp_cloud`, one target each, alongside the existing eight
channels. Heartbeat is separate. All four default disabled. Contract tests use
synthetic accounts and transports; real APIs, recipient confirmation, billing,
native ARM64 and production operation are **NOT RUN**. Platform acceptance does
not prove delivery or reading. This feature does not register accounts, accept
terms, buy numbers, submit templates or obtain recipient consent for you.

## Configure and preview

Use the existing terminal setup, then **Notification channels**. For each brand:

1. Open **Protected credentials**, select Replace and enter individual hidden
   account/token/sender/recipient fields. Blank retains an existing field;
   `clear_fields` explicitly clears named fields. Clearing the entire snapshot
   requires disabling the channel. Cancel preserves the previous state.
2. Record **Recipient subscription**: the actual notification purpose, selected
   types (`event`, `daily`, `test`) and a bounded local evidence identifier.
   Record creates a local timestamp and new consent basis. It does not read a
   consent document or prove legal compliance. Keep preserves the basis; Revoke
   immediately suppresses unsent notifications. Paid channels require explicit
   cost confirmation. Verify platform authorization independently.
3. Configure independent `en` / `zh`, event minimum severity and daily summaries,
   finite limits and enablement. Saving uses draft → validate → review → apply /
   rollback and never performs token exchange, account lookup or message sending.
4. Preview the selected target locally:

   ```bash
   sudo noderampart notify preview --channel twilio_sms
   ```

   Preview shows the masked target, frozen content/template, estimated segments,
   finite limits and unknown actual charge. It neither queues nor reserves a
   charge. An unconsented, invalid or incompatible request is refused locally.
5. A selected paid test requires the current preview ID and separate confirmation:

   ```bash
   sudo noderampart notify test --channel twilio_sms --confirm-paid --preview-id <current-preview-id>
   ```

   The TUI performs the same preview/confirmation. For an enabled QQ/LINE target,
   use `notify test --channel qqbot` / `line`. Every test is one synthetic logical
   notification sharing subscription, persistent intent, limits and expiry.
   No credentials or recipient addresses belong in CLI arguments.

Ordinary configuration contains only the credential-file reference and local
policy. Managed random credential files are readable by the actual daemon UID,
mode 0600, within a protected service-group directory. JSON decoding rejects
unknown/duplicate fields, symlinks, multiple links, unsafe parents, permissions,
unstable files and oversized content. Configuration/status/errors do not reveal
credentials. Credential replacement creates a new delivery identity and isolates
old pending bodies; A → B → A cannot resurrect isolated A messages. Normal QQ
short-token refresh preserves identity and budgets. Changing policy also isolates
incompatible pending bodies. Language/timezone changes do not retranslate old
messages or change archived report periods.

## QQ Bot: active C2C or group messages

Prerequisites: your own approved QQ Open Platform bot/application with AppID and
AppSecret, active-send capability for the chosen scene, and a recipient/group
that permits these notifications. The protected fields are `app_id`, `app_secret`,
`target_type` (`user` / `group`) and the corresponding platform-issued `target_id`.
An openid/group_openid is **not** a QQ number/group number or a convertible public
identifier. Import an ID obtained through an authorized official event source or
existing integration under the same bot identity. This release does not deploy an
event callback, join a Gateway session, enumerate recipients or invent a console
screen for retrieving group IDs. If you cannot obtain an authorized ID or active
permission, leave the channel disabled: **ACCOUNT_AUTHORIZATION_REQUIRED**.

The current official API origin is `https://api.bot.qq.com`. AppID/AppSecret are
exchanged at `/app/getAppAccessToken`; messages use `/v2/users/{openid}/messages`
or `/v2/groups/{group_openid}/messages`, `QQBot` access authentication and the
operator's application identity. Only plain active text is used: no fabricated
reply `msg_id`, `event_id` or reply-window quota. Tokens stay in a bounded memory
cache, refresh ahead of expiry through a single-flight gate, and share the
current dispatch deadline. An explicit authentication rejection allows at most
one authorized refresh attempt. Unknown message submission is held instead of
resubmitted; `msg_seq` is not assumed to be durable active-message idempotency.

Official numeric business errors distinguish refusal, permission, quota/rate
limits and receipt failure. Local text is bounded to 1800 UTF-8 bytes; this is an
application bound, **not a claimed official character allowance**. Platform
scene/content/domain restrictions still apply. Disable locally, revoke secrets /
recipient authorization on the platform and replace protected credentials to
rotate. Local resume cannot grant missing platform capabilities.

## LINE: Official Account Messaging API Push

Use a LINE Official Account and a Messaging API channel with a console-issued
long-lived Channel Access Token. Protected fields: `channel_access_token`,
`target_type` (`user`, `group`, `room`) and `target_id`. IDs are the channel's
platform-issued U/C/R identifiers, not a searchable LINE ID, nickname or phone.
Your own user ID can be obtained from the relevant provider's developer console;
group/room IDs require authorized official events in a group containing the bot.
No callback or recipient discovery is supplied here. Verify push eligibility,
recipient consent, account plan/monthly quota and platform terms before enabling.

Push uses `https://api.line.me/v2/bot/message/push`, Bearer authentication and one
plain-text message. The UUID `X-Line-Retry-Key` is generated and persisted before
the first request, with exact target/content/order. A valid duplicate-acceptance
409 requires the expected official response and accepted-request ID; arbitrary
409 is not success. Retries stop before the official 24-hour window (local bound
23h55m), including restart and clock rollback; no new key silently resends an old
request. HTTP 200 / duplicate acceptance confirms processing only: a blocked or
deleted account may never receive it. Monthly quota exhaustion pauses submission.
A new manual token/target snapshot isolates old messages. No LINE Notify, reply
API, broadcast or multicast is implemented. UTF-16 counting and local 1800-byte
presentation bounds keep final text below the official push text limit.

## Twilio SMS: individual consent and finite segment budget

Use your authorized Twilio account and a sender usable in the recipient's region:
exactly one E.164 `from` or `messaging_service_sid`; recipient `to` is E.164 without
`whatsapp:` or another channel prefix. Recommended protected auth is `account_sid`,
`auth_mode=api_key`, `api_key_sid`, `api_key_secret`; explicitly selected
`auth_mode=auth_token` uses `account_sid` and `auth_token`. Keys must permit the
send and subsequent specific-message reads. No secret is sent in a URL.

The original HTTPS Messages API uses Basic authentication and form encoding at
`/2010-04-01/Accounts/{AccountSid}/Messages.json`. A matching account/recipient/body
and valid Message SID are required for acceptance. The SID is persisted; later
bounded GETs read only that Account/SID, never POST the body again or search
messages by phone/text. Up to eight polls within 24h survive restart. `queued`,
`sending`, `sent`, `delivered`, `undelivered`, `failed` and other explicitly handled
states remain distinct; sent is not delivered and delivered is not read. Missing,
unknown or invalid observations do not invent a final delivery status.

Default local UTC-day limits are **20 logical submissions / 40 estimated
segments**, maximum **2 segments per notification**, high-severity events and
**daily summaries off**. GSM-7 extension characters cost two septets; non-GSM text
uses UTF-16 code units, including emoji surrogate pairs. Single-message estimates
use 160/70; multipart estimates conservatively use 152/66 for toll-free / unknown
Messaging Service sender cases. The final identity, essential metrics, coverage,
local command and STOP footer are counted before admission. Host shortening is
visible; essential content that cannot fit is rejected locally. No uncontrolled
splitting or Smart Encoding/URL shortening/MMS fallback is enabled. Platform
`num_segments`, price and currency are observations, not the local reservation;
missing price remains **unknown**. External encoding/account settings may change
actual billing. Local limits are not an account-dollar ceiling.

Enable only with applicable number/account verification, explicit opt-in,
regional rules and U.S. A2P 10DLC where applicable; sending only to yourself does
not remove those requirements. Arrange appropriate platform STOP / Advanced
Opt-Out handling. This program receives no inbound SMS. An operator must process
other withdrawals and revoke locally. Error **21610**, synchronous or polled,
persists an opt-out lock across restart, rotation and ordinary resume. Only a new
consent basis after the recipient completes the official permitted recovery path
can qualify for restoration; no opt-out override or sender-switch workaround is
called. Platform validity is limited to the smaller of five minutes and remaining
local TTL. Already accepted requests cannot be withdrawn by local disable.

## WhatsApp Cloud: approved BODY templates only

Use your own authorized WhatsApp Business Platform assets and a server-side token
with sending permission (normally `whatsapp_business_messaging`). The protected
fields are `phone_number_id` (the platform ID, not sender phone), `access_token`,
`recipient` (international digits) and fixed `graph_version=v26.0`. That version
was checked against the official version table on 2026-10-02; no latest-following
URL, query token or arbitrary Graph destination is accepted. Short-lived console
test tokens are unsuitable unattended credentials. Even nominal long-lived
system-user tokens can be revoked or lose asset permission. General business
management / template discovery permission is not requested just to send. This
first version requires a token with messaging access to one message account for
the selected business phone. Multiple message accounts requiring
`messaging_account_id` are outside its contract; administrators must use an
authorized single-account scope, rather than letting this program guess an account.
The protected recipient holds full international digits; the request adds `+`
explicitly so the business phone country is never prepended implicitly.

Provide approved **event**, **daily** and **test** routes under `templates`, each
with `name`, actual approved `language`, and ordered `parameters`. Only positional
BODY text parameters are supported, drawn from:
`host_alias,event_kind,phase,severity,time,bounded_summary,local_reference`.
Every route must contain each of the six latter slots exactly once; `host_alias`
is optional. Slot order must match the approved template. Coverage is included
in `bounded_summary`; a static template that omits severity/time/local reference
is rejected locally.
Routes may reuse a template only when its approved structure genuinely matches.
Notification `en`/`zh` is independent of the approved platform language code;
a missing Chinese route cannot silently become English or fabricate approval.
Name/language/ordered slots/values and a structure fingerprint freeze at enqueue.
No free-text fallback, chat-window guess, media, dynamic URL button or marketing /
authentication flow is added. Parameters are bounded scalars without tabs/newlines;
no report/log is hidden inside one parameter.

Proposed templates to submit yourself (approval and Utility classification are
**not guaranteed**):

```text
English BODY: NodeRampart {{1}}: {{2}} / {{3}}. Severity {{4}}, time {{5}}.
{{6}}. Local reference: {{7}}.
Chinese BODY: NodeRampart {{1}}：{{2}}／{{3}}。级别 {{4}}，时间 {{5}}。
{{6}}。本地查看：{{7}}。
Parameter order: host_alias,event_kind,phase,severity,time,bounded_summary,local_reference
```

Use separate approved language versions and appropriate test/daily wording or
compatible templates; submit them in the authorized WABA's official manager.
Request `POST https://graph.facebook.com/v26.0/{phone_number_id}/messages` is
`type=template` with Bearer authentication. A valid recipient association and
`wamid` acknowledge acceptance only. There is no supported GET by wamid for
`delivered`/`read` in this release; no callback listener is created. Template
rejection, pause, disabling, language/parameter mismatch, permission, recipient,
quality/policy and rate limitations stop at their observed scope. No template
or sender substitution evades policy. Defaults: 20 logical templates per UTC day,
high-severity events, daily off. Provide a reachable support/withdrawal route and
actually process recipient opt-outs; this program cannot observe inbound STOP
or replies and does not claim all template types have automatic STOP processing.
Self-hosting a user-owned account does not grant SaaS/Tech Provider approval.
The official platform page prohibits unauthorized third-party tools. Operators
must establish the rights applicable to their own app, assets and this use.
Neither an enable checkbox nor the MIT source license supplies platform approval
or resolves a prohibited use. Some additional Meta Platform Terms pages returned
a login/throttling page during this review; their unread text is not treated as
a commercial distribution grant. Account authorization remains a separate gate.
The local minimum interval is six seconds per recipient; a 131056 pair limit
uses bounded exponential backoff without shortening provider waiting periods.

## Durable states, recovery and outbound boundary

`queued` is local; a committed dispatch intent/reservation precedes external I/O.
`accepted` is a provider receipt; `delivery_unknown` means possible external
acceptance without reliable confirmation. Non-idempotent unresolved QQ, Twilio
and WhatsApp requests never automatically resubmit after lease expiry, shutdown,
response loss or receipt-write failure. LINE uses its persisted key only inside
its valid window. View `notify list`, `notify status`, timeline and diagnostic
fields. Unknown requests can be inspected/quarantined; ordinary retry/resume/test
cannot silently revive them. This is not exactly-once delivery. Requests already
in flight can finish at the original target after local changes.

Reservations remain consumed on uncertainty. Fixed channel/account-scope UTC
buckets survive rotation, enable/disable, report timezone changes and restart;
clock rollback fails closed. Global outbox remains 10,000 entries / 32 MiB,
with per-channel shares and reserved capacity for other enabled targets. Frozen
payload bytes count. A failed/held target does not get twelve times that budget.
No remote I/O occurs inside SQLite writes, monitor locks or config apply locks.

Schema **13 → 14** is one transaction preserving old queues, decisions and
presentation context. Config/API 1 and sensor 5 remain unchanged; older binaries
reject schema 14. Use matching old database/config/credential backups to roll
back, never downgrade in place. Product-supported backup restore holds paid
channels for explicit receipt/budget reconciliation. `notify reconcile-paid
--channel twilio_sms --evidence-ref <local-id> --confirm` only permits future
notifications after verification, conservatively consumes the current UTC-day
allowance and leaves historical uncertainty/opt-out facts intact. Disk/ledger
rollback cannot prove the platform never accepted later messages.

Only fixed official HTTPS/443 origins are constructed in code. TLS, DNS and
connected public addresses are checked; no redirects, private/link-local targets,
environment proxies, new listener, downloaded runtime code or extra daemon
capability is introduced. Old Telegram/generic Webhook/heartbeat contracts and
proxy behavior stay unchanged. Credential and response handling is bounded;
public evidence uses fixed states and fresh pseudonyms, not raw account/phone/
provider IDs. Ordinary uninstall preserves data. Purge deletes only verified
product-owned paths and can fail partway; it is not an atomic rollback.

## Official references and remaining contract limits

Checked **2026-10-02**. API contracts, MIT source license, platform terms,
organization administration and recipient consent are separate obligations.
NodeRampart claims no vendor certification or inherited OpenClaw identity.
Original standard-library implementations introduce no vendor SDK/dependency.

- [QQ current API overview](https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/overview.html),
  [QQ developer portal](https://bot.q.qq.com/wiki/), official current token/C2C/group
  pages reached through that portal, and [Tencent's technical reference](https://github.com/tencent-connect/openclaw-qqbot).
  The complete current developer-service agreement and a universal text-length
  unit were not confirmed by readable public pages. Operator approval/scene
  entitlement is still required; a plugin's code license does not grant it.
- [LINE Messaging API reference](https://developers.line.biz/en/reference/messaging-api/),
  [retry contract](https://developers.line.biz/en/docs/messaging-api/retrying-api-request/),
  [ID acquisition](https://developers.line.biz/en/docs/messaging-api/getting-user-ids/),
  [terms](https://developers.line.biz/en/terms-and-policies/).
- [Twilio Messages](https://www.twilio.com/docs/messaging/api/message-resource),
  [segment rules](https://www.twilio.com/docs/glossary/what-sms-character-limit),
  [21610](https://www.twilio.com/docs/api/errors/21610),
  [Advanced Opt-Out](https://www.twilio.com/docs/messaging/tutorials/advanced-opt-out),
  [A2P 10DLC](https://www.twilio.com/docs/messaging/compliance/a2p-10dlc),
  [messaging policy](https://www.twilio.com/en-us/legal/messaging-policy),
  [service terms](https://www.twilio.com/en-us/legal/tos).
- [Meta Cloud API](https://developers.facebook.com/docs/whatsapp/cloud-api/),
  [Graph versions](https://developers.facebook.com/docs/graph-api/changelog/versions/),
  [official API collection](https://www.postman.com/meta/whatsapp-business-platform/collection/wlk6lh4/whatsapp-cloud-api),
  [WhatsApp business messaging policy](https://business.whatsapp.com/policy),
  [business terms](https://www.whatsapp.com/legal/business-terms/).
  Some developer pages were rate-limited; supplementary official collection /
  readable policy/version material supplied the verified contract. Meta's
  additional linked terms can require login; no inaccessible page is claimed
  fully read. Numeric bounds here are local controls unless expressly identified
  as an official rule. Operators must review their actual current account terms,
  template/number eligibility, category/pricing and country restrictions.

Daily compact paid notifications retain the archived interval, end exclusive;
`2026-03-08-0500/09-0400` abbreviates the identical end year/month and retains
both DST offsets. Non-midnight endpoints include their actual clock time. B
means bytes; KiB/MiB/GiB are binary byte units and `~` marks a rounded compact
value. Coverage unknown/partial and the local report command remain present.

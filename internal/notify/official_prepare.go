// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
)

const maxOfficialFrozenBytes = 4096

type officialFrozen struct {
	Channel     string                  `json:"channel"`
	Kind        string                  `json:"kind"`
	Fingerprint string                  `json:"fingerprint"`
	Body        string                  `json:"body,omitempty"`
	Template    *officialFrozenTemplate `json:"template,omitempty"`
}
type officialFrozenTemplate struct {
	Name     string   `json:"name"`
	Language string   `json:"language"`
	Slots    []string `json:"slots"`
	Values   []string `json:"values"`
}

func safeOfficialText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return (unicode.IsControl(r) && r != '\n') || unicode.Is(unicode.Cf, r) }) < 0
}

func (s *Official) policyAllows(kind string) bool {
	return s.cfg.Subscription.Allows(kind) && s.cfg.DailyMessageLimit > 0 && (!config.IsPaidOfficialChannel(s.channel) || s.cfg.Subscription.CostConfirmed) && (kind == "test" || kind == "event" && s.cfg.EventsEnabled || kind == "daily" && s.cfg.DailyEnabled)
}

// PrepareMessage freezes a bounded request without reading a file or accessing
// the network. Its output is admitted atomically with the durable dispatch row.
func (s *Official) PrepareMessage(message *store.OutboxMessage) error {
	if message == nil || message.Channel != s.channel || message.Destination != s.destination || !s.policyAllows(message.LogicalKind) {
		return errors.New("official notification policy or target does not allow preparation")
	}
	frozen := officialFrozen{Channel: s.channel, Kind: message.LogicalKind, Fingerprint: s.fingerprint}
	if s.channel == "twilio_sms" || s.channel == "whatsapp_cloud" {
		semantic, err := decodeOfficialSemantic(message.SemanticPayload)
		if err != nil {
			return err
		}
		if s.channel == "twilio_sms" {
			body, encoding, segments, err := RenderOfficialSMS(semantic, message.Language, s.cfg.MaxSegments)
			if err != nil {
				return err
			}
			message.Body, message.Encoding, message.EstimatedSegments = body, encoding, segments
			frozen.Body = body
		} else {
			template := s.template(message.LogicalKind)
			if template == nil {
				return errors.New("approved WhatsApp template is unavailable")
			}
			prepared := &officialFrozenTemplate{Name: template.Name, Language: template.Language, Slots: append([]string{}, template.Parameters...), Values: make([]string, 0, len(template.Parameters))}
			for _, slot := range template.Parameters {
				value := officialTemplateLiteral(semantic.slot(slot))
				if !safeOfficialText(value, 1024) || strings.ContainsAny(value, "\n\t") {
					return errors.New("WhatsApp template parameter is invalid or exceeds its bound")
				}
				prepared.Values = append(prepared.Values, value)
			}
			frozen.Template = prepared
		}
	} else {
		if !safeOfficialText(message.Body, 1800) || s.channel == "line" && officialUTF16Units(message.Body) > 5000 {
			return errors.New("official text is invalid or exceeds its bound")
		}
		frozen.Body = message.Body
	}
	encoded, err := json.Marshal(frozen)
	if err != nil || len(encoded) > maxOfficialFrozenBytes {
		return errors.New("official frozen request exceeds its bound")
	}
	if s.frozenHasSecret(frozen) {
		return errors.New("official frozen request contains protected credential data")
	}
	message.FrozenPayload = string(encoded)
	return nil
}

func decodeOfficialSemantic(value string) (OfficialSemantic, error) {
	var semantic OfficialSemantic
	object, err := nativeJSONObject([]byte(value))
	if err != nil || len(value) > 4096 || len(object) != 8 {
		return semantic, errors.New("official semantic summary is invalid")
	}
	limits := map[string]int{"host_alias": 80, "event_kind": 80, "phase": 40, "severity": 40, "time": 80, "bounded_summary": 1024, "local_reference": 160, "coverage": 160}
	for key, raw := range object {
		limit, ok := limits[key]
		var field *string
		if !ok || json.Unmarshal(raw, &field) != nil || field == nil || !safeOfficialText(*field, limit) {
			return semantic, errors.New("official semantic field is invalid or exceeds its bound")
		}
	}
	if json.Unmarshal([]byte(value), &semantic) != nil {
		return semantic, errors.New("official semantic summary is invalid")
	}
	return semantic, nil
}
func (s OfficialSemantic) slot(name string) string {
	switch name {
	case "host_alias":
		return s.HostAlias
	case "event_kind":
		return s.EventKind
	case "phase":
		return s.Phase
	case "severity":
		return s.Severity
	case "time":
		return s.Time
	case "bounded_summary":
		return s.BoundedSummary + "; " + s.Coverage
	case "local_reference":
		return s.LocalReference
	}
	return ""
}
func (s *Official) template(kind string) *config.OfficialTemplate {
	if s.credential.Templates == nil {
		return nil
	}
	switch kind {
	case "event":
		return s.credential.Templates.Event
	case "daily":
		return s.credential.Templates.Daily
	case "test":
		return s.credential.Templates.Test
	}
	return nil
}

func (s *Official) frozen(message store.OutboxMessage) (officialFrozen, error) {
	var frozen officialFrozen
	invalid := errors.New("official persisted request or target is invalid")
	if message.ID == "" || message.Channel != s.channel || message.Destination != s.destination || !s.policyAllows(message.LogicalKind) || len(message.FrozenPayload) > maxOfficialFrozenBytes {
		return frozen, invalid
	}
	object, err := nativeJSONObject([]byte(message.FrozenPayload))
	if err != nil {
		return frozen, invalid
	}
	for key, raw := range object {
		if key != "channel" && key != "kind" && key != "fingerprint" && key != "body" && key != "template" || string(raw) == "null" {
			return frozen, invalid
		}
	}
	if json.Unmarshal([]byte(message.FrozenPayload), &frozen) != nil || frozen.Channel != s.channel || frozen.Kind != message.LogicalKind || frozen.Fingerprint != s.fingerprint {
		return frozen, invalid
	}
	if s.frozenHasSecret(frozen) {
		return frozen, invalid
	}
	if s.channel == "whatsapp_cloud" {
		t := s.template(message.LogicalKind)
		p := frozen.Template
		if t == nil || p == nil || frozen.Body != "" || t.Name != p.Name || t.Language != p.Language || len(t.Parameters) != len(p.Slots) || len(p.Slots) != len(p.Values) {
			return frozen, invalid
		}
		templateObject, err := nativeJSONObject(object["template"])
		if err != nil || len(templateObject) != 4 {
			return frozen, invalid
		}
		for key := range templateObject {
			if key != "name" && key != "language" && key != "slots" && key != "values" {
				return frozen, invalid
			}
		}
		for i, slot := range p.Slots {
			if slot != t.Parameters[i] || !safeOfficialText(p.Values[i], 1024) || strings.ContainsAny(p.Values[i], "\n\t") || p.Values[i] != officialTemplateLiteral(p.Values[i]) {
				return frozen, invalid
			}
		}
	} else {
		if frozen.Template != nil || !safeOfficialText(frozen.Body, 1800) || frozen.Body != message.Body || s.channel == "line" && officialUTF16Units(frozen.Body) > 5000 {
			return frozen, invalid
		}
		if s.channel == "twilio_sms" {
			encoding, segments, err := EstimateSMS(frozen.Body)
			if err != nil || encoding != message.Encoding || segments != message.EstimatedSegments || segments > s.cfg.MaxSegments || segments < 1 {
				return frozen, invalid
			}
		}
	}
	return frozen, nil
}

// Fixed approved template structure controls presentation. Dynamic fields use
// visible punctuation in place of WhatsApp's formatting delimiters.
func officialTemplateLiteral(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '*':
			r = '＊'
		case '_':
			r = '＿'
		case '~':
			r = '～'
		case '`':
			r = '｀'
		case '\\':
			r = '＼'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *Official) Dispatch(ctx context.Context, message store.OutboxMessage) (DispatchOutcome, error) {
	if message.DispatchState != "in_flight" || message.DispatchAttempt < 1 || message.FirstAttemptAt.IsZero() {
		return DispatchOutcome{State: DispatchRejected, Reason: "durable_intent_required"}, errors.New("official durable dispatch intent is required")
	}
	if s.now().Before(message.FirstAttemptAt) {
		return DispatchOutcome{State: DispatchRejected, Reason: "clock_rollback", Suspend: true}, errors.New("official dispatch clock precedes its durable intent")
	}
	if message.ExpiresAt.IsZero() || !s.now().Before(message.ExpiresAt) {
		return DispatchOutcome{State: DispatchRejected, Reason: "message_expired"}, errors.New("official dispatch expiry is invalid")
	}
	frozen, err := s.frozen(message)
	if err != nil {
		return DispatchOutcome{State: DispatchRejected, Reason: "frozen_request_invalid"}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout.Duration)
	defer cancel()
	var out DispatchOutcome
	switch s.channel {
	case "qqbot":
		out, err = s.dispatchQQ(ctx, frozen)
	case "line":
		out, err = s.dispatchLINE(ctx, message, frozen)
	case "twilio_sms":
		out, err = s.dispatchTwilio(ctx, message, frozen)
	case "whatsapp_cloud":
		out, err = s.dispatchWhatsApp(ctx, message, frozen)
	default:
		return DispatchOutcome{State: DispatchRejected, Reason: "unsupported_channel"}, errors.New("unsupported official notification channel")
	}
	return s.safeOutcome(out), err
}

func (s *Official) Poll(ctx context.Context, message store.OutboxMessage) (DispatchOutcome, error) {
	if s.channel != "twilio_sms" || message.DispatchState != "accepted" || message.ProviderID == "" || message.PollAttempts < 1 || message.PollAttempts > 8 || message.FirstAttemptAt.IsZero() || s.now().Before(message.FirstAttemptAt) || message.PollDeadline.IsZero() || message.PollDeadline.After(message.FirstAttemptAt.Add(24*time.Hour)) || !s.now().Before(message.PollDeadline) {
		return DispatchOutcome{State: DispatchRejected, Reason: "poll_intent_invalid"}, errors.New("official durable poll intent is invalid")
	}
	if _, err := s.frozen(message); err != nil {
		return DispatchOutcome{State: DispatchRejected, Reason: "frozen_request_invalid"}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout.Duration)
	defer cancel()
	out, err := s.pollTwilio(ctx, message)
	return s.safeOutcome(out), err
}

func (s *Official) containsCredential(value string) bool {
	for _, protected := range []string{s.credential.AppID, s.credential.AppSecret, s.credential.TargetID, s.credential.ChannelAccessToken, s.credential.AccountSID, s.credential.APIKeySID, s.credential.APIKeySecret, s.credential.AuthToken, s.credential.From, s.credential.To, s.credential.MessagingServiceSID, s.credential.PhoneNumberID, s.credential.AccessToken, s.credential.Recipient} {
		if len(protected) >= 8 && strings.Contains(value, protected) {
			return true
		}
	}
	return false
}
func (s *Official) containsSecret(value string) bool {
	for _, secret := range []string{s.credential.AppSecret, s.credential.ChannelAccessToken, s.credential.APIKeySecret, s.credential.AuthToken, s.credential.AccessToken} {
		if secret != "" && strings.Contains(value, secret) {
			return true
		}
	}
	return false
}
func (s *Official) frozenHasSecret(frozen officialFrozen) bool {
	if s.containsSecret(frozen.Body) {
		return true
	}
	if frozen.Template != nil {
		for _, value := range frozen.Template.Values {
			if s.containsSecret(value) {
				return true
			}
		}
	}
	return false
}
func (s *Official) safeOutcome(out DispatchOutcome) DispatchOutcome {
	value := out.ProviderID + "\n" + out.ProviderState + "\n" + out.Reason + "\n" + out.PriceUnit
	if out.Price != nil {
		value += "\n" + *out.Price
	}
	if s.containsCredential(value) {
		return DispatchOutcome{State: DispatchUnknown, Reason: "unconfirmed_response", StatusCode: out.StatusCode, RetryAfterDuration: out.RetryAfterDuration, Suspend: out.Suspend}
	}
	return out
}

// LINE retry expiry also guards against a clock moving before the first intent.
func officialRetryWindow(first, now time.Time) bool {
	return !first.IsZero() && !now.Before(first) && now.Before(first.Add(24*time.Hour))
}

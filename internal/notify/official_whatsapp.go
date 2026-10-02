// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

func (s *Official) dispatchWhatsApp(ctx context.Context, message store.OutboxMessage, frozen officialFrozen) (DispatchOutcome, error) {
	parameters := make([]map[string]string, 0, len(frozen.Template.Values))
	for _, value := range frozen.Template.Values {
		parameters = append(parameters, map[string]string{"type": "text", "text": value})
	}
	template := map[string]any{"name": frozen.Template.Name, "language": map[string]string{"code": frozen.Template.Language}}
	if len(parameters) > 0 {
		template["components"] = []any{map[string]any{"type": "body", "parameters": parameters}}
	}
	request, err := officialJSONRequest(ctx, http.MethodPost, "https://graph.facebook.com/"+s.credential.GraphVersion+"/"+s.credential.PhoneNumberID+"/messages", map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": "+" + s.credential.Recipient, "type": "template", "template": template})
	if err != nil {
		return DispatchOutcome{State: DispatchRejected, Reason: "payload_invalid"}, err
	}
	request.Header.Set("Authorization", "Bearer "+s.credential.AccessToken)
	status, headers, data, err := s.request(ctx, request)
	out := officialHTTPFailure(status, headers, s.now())
	if err != nil {
		return out, err
	}
	if status == 429 || status >= 500 || status == 408 || status >= 300 && status < 400 || status >= 400 && status != 400 {
		return out, nil
	}
	object, err := nativeJSONObject(data)
	if err != nil {
		return out, nil
	}
	if raw, exists := object["error"]; exists {
		providerError, err := nativeJSONObject(raw)
		if err != nil {
			return out, nil
		}
		code, valid := officialInteger(providerError["code"])
		if !valid || code < 0 {
			return out, nil
		}
		out.APIErrorCode = code
		out.State = DispatchRejected
		out.Reason = "provider_rejected"
		knownCode := true
		switch {
		case code == 0 || code == 3 || code == 10 || code == 190 || code >= 200 && code <= 299 || code == 131005:
			out.Reason = "credential_or_permission_rejected"
			out.Suspend = true
		case code == 4 || code == 80007 || code == 130429 || code == 131056:
			out.State = DispatchNotAccepted
			out.Reason = "rate_limited"
			if out.RetryAfterDuration < time.Minute {
				out.RetryAfterDuration = time.Minute
			}
			if code == 131056 {
				delay, suspend := officialWhatsAppPairDelay(message.DispatchAttempt)
				out.Suspend = out.Suspend || suspend
				if delay > out.RetryAfterDuration {
					out.RetryAfterDuration = delay
				}
			}
		case code == 131048 || code == 131064:
			out.State = DispatchNotAccepted
			out.Reason = "quality_or_policy_limited"
			out.Suspend = true
		case code == 132015 || code == 132016:
			out.Reason = "template_unavailable"
			out.Suspend = true
		case code == 132000:
			out.Reason = "template_parameter_count_invalid"
		case code == 132001:
			out.Reason = "template_missing_or_unapproved"
			out.Suspend = true
		case code == 132005:
			out.Reason = "template_text_too_long"
		case code == 132012:
			out.Reason = "template_parameter_format_invalid"
		case code == 132007:
			out.Reason = "template_policy_rejected"
			out.Suspend = true
		case code == 1 || code == 2 || code == 131000 || code == 131016:
			out.State = DispatchUnknown
			out.Reason = "unconfirmed_provider_failure"
		default:
			knownCode = false
		}
		if raw, exists := providerError["is_transient"]; exists {
			var transient *bool
			if json.Unmarshal(raw, &transient) != nil || transient == nil {
				out.State = DispatchUnknown
				out.Reason = "unconfirmed_response"
			} else if *transient && !knownCode {
				out.State = DispatchUnknown
				out.Reason = "unconfirmed_provider_failure"
			}
		}
		return out, nil
	}
	if status != 200 {
		return out, nil
	}
	product, validProduct := officialString(object["messaging_product"])
	if !validProduct || product != "whatsapp" {
		return out, nil
	}
	var messages []json.RawMessage
	if json.Unmarshal(object["messages"], &messages) != nil || len(messages) != 1 {
		return out, nil
	}
	acknowledgment, err := nativeJSONObject(messages[0])
	if err != nil {
		return out, nil
	}
	id, idOK := officialString(acknowledgment["id"])
	if !idOK || !strings.HasPrefix(id, "wamid.") || len(id) <= 6 || !officialReceiptID(id) {
		return out, nil
	}
	providerState := "api_accepted"
	if raw, exists := acknowledgment["message_status"]; exists {
		value, ok := officialString(raw)
		if !ok || value != "accepted" && value != "held_for_quality_assessment" {
			return out, nil
		}
		providerState = value
	}
	if raw, exists := object["contacts"]; exists {
		var contacts []json.RawMessage
		if json.Unmarshal(raw, &contacts) != nil || len(contacts) != 1 {
			return out, nil
		}
		contact, err := nativeJSONObject(contacts[0])
		if err != nil {
			return out, nil
		}
		input, ok := officialString(contact["input"])
		if !ok || input != s.credential.Recipient && input != "+"+s.credential.Recipient {
			return out, nil
		}
		if raw, exists := contact["wa_id"]; exists {
			waID, ok := officialString(raw)
			if !ok || waID != s.credential.Recipient {
				return out, nil
			}
		}
	}
	return DispatchOutcome{State: DispatchAccepted, ProviderID: id, ProviderState: providerState, Reason: "api_accepted", StatusCode: status}, nil
}

func officialWhatsAppPairDelay(attempt int) (time.Duration, bool) {
	if attempt < 1 || attempt > 10 {
		return 0, true
	}
	delay := time.Second
	for range attempt - 1 {
		if delay > maxTelegramRetryAfter/4 {
			return 0, true
		}
		delay *= 4
	}
	return delay, false
}

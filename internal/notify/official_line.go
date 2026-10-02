// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/littlesho/NodeRampart/internal/store"
)

var officialUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func (s *Official) dispatchLINE(ctx context.Context, message store.OutboxMessage, frozen officialFrozen) (DispatchOutcome, error) {
	if !officialUUID.MatchString(message.RetryKey) || !officialRetryWindow(message.FirstAttemptAt, s.now()) {
		return DispatchOutcome{State: DispatchRejected, Reason: "retry_window_expired", Suspend: true}, errors.New("LINE durable retry key or window is invalid")
	}
	request, err := officialJSONRequest(ctx, http.MethodPost, "https://api.line.me/v2/bot/message/push", map[string]any{"to": s.credential.TargetID, "messages": []any{map[string]string{"type": "text", "text": frozen.Body}}})
	if err != nil {
		return DispatchOutcome{State: DispatchRejected, Reason: "payload_invalid"}, err
	}
	request.Header.Set("Authorization", "Bearer "+s.credential.ChannelAccessToken)
	request.Header.Set("X-Line-Retry-Key", message.RetryKey)
	status, headers, data, err := s.request(ctx, request)
	out := officialHTTPFailure(status, headers, s.now())
	if err != nil {
		return out, err
	}
	if status == 200 || status == 409 {
		out.State = DispatchUnknown
		out.Reason = "unconfirmed_response"
		id, valid := lineReceipt(data)
		if status == 409 {
			accepted := headers.Get("X-Line-Accepted-Request-Id")
			object, parseErr := nativeJSONObject(data)
			reason, reasonOK := officialString(object["message"])
			if parseErr != nil || !officialUUID.MatchString(accepted) || !reasonOK || reason != "The retry key is already accepted" {
				valid = false
			} else if _, hasSentMessages := object["sentMessages"]; !hasSentMessages {
				id, valid = accepted, true
			}
		}
		if valid {
			return DispatchOutcome{State: DispatchAccepted, ProviderID: id, ProviderState: "api_accepted", Reason: "api_accepted", StatusCode: status}, nil
		}
	}
	if status == 429 {
		object, e := nativeJSONObject(data)
		if e == nil {
			reason, ok := officialString(object["message"])
			if ok && (reason == "You have reached your monthly limit." || reason == "You have reached your monthly limit") {
				out.Suspend = true
				out.Reason = "monthly_quota_exhausted"
			}
		}
	}
	return out, nil
}

func lineReceipt(data []byte) (string, bool) {
	object, err := nativeJSONObject(data)
	if err != nil {
		return "", false
	}
	var messages []json.RawMessage
	if json.Unmarshal(object["sentMessages"], &messages) != nil || len(messages) != 1 {
		return "", false
	}
	message, err := nativeJSONObject(messages[0])
	if err != nil {
		return "", false
	}
	raw := message["id"]
	value := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var ok bool
		value, ok = officialString(raw)
		if !ok {
			return "", false
		}
	}
	if len(value) < 1 || len(value) > 64 || strings.TrimLeft(value, "0") == "" || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", false
	}
	return value, true
}

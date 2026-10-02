// SPDX-License-Identifier: MIT

package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func officialString(raw json.RawMessage) (string, bool) {
	var value *string
	err := json.Unmarshal(raw, &value)
	if err != nil || value == nil {
		return "", false
	}
	return *value, true
}
func officialInteger(raw json.RawMessage) (int, bool) {
	var value *int
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return 0, false
	}
	return *value, true
}
func officialDecimal(raw json.RawMessage, maximum int64) (int64, bool) {
	var value string
	if len(raw) > 0 && raw[0] == '"' {
		var ok bool
		value, ok = officialString(raw)
		if !ok {
			return 0, false
		}
	} else {
		value = string(raw)
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil && n > 0 && n <= maximum && strconv.FormatInt(n, 10) == value
}
func officialJSONRequest(ctx context.Context, method, endpoint string, payload any) (*http.Request, error) {
	data, err := json.Marshal(payload)
	if err != nil || len(data) > 16<<10 {
		return nil, errors.New("official encoded request exceeds its bound")
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("official request could not be constructed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "NodeRampart")
	return request, nil
}

func (s *Official) qqAccess(ctx context.Context) (string, DispatchOutcome, error) {
	select {
	case s.qqTokenGate <- struct{}{}:
	case <-ctx.Done():
		return "", DispatchOutcome{State: DispatchNotAccepted, Reason: "token_wait_canceled"}, ctx.Err()
	}
	defer func() { <-s.qqTokenGate }()
	if s.qqToken != "" && s.now().Before(s.qqTokenUntil.Add(-60*time.Second)) {
		return s.qqToken, DispatchOutcome{}, nil
	}
	request, err := officialJSONRequest(ctx, http.MethodPost, "https://api.bot.qq.com/app/getAppAccessToken", map[string]string{"appId": s.credential.AppID, "clientSecret": s.credential.AppSecret})
	if err != nil {
		return "", DispatchOutcome{State: DispatchRejected, Reason: "token_request_invalid"}, err
	}
	status, headers, data, err := s.request(ctx, request)
	out := officialHTTPFailure(status, headers, s.now())
	// A failed token request cannot have submitted a recipient message.
	if out.State == DispatchUnknown {
		out.State = DispatchNotAccepted
		out.Reason = "token_unavailable"
	}
	if err != nil {
		return "", out, err
	}
	if status != http.StatusOK {
		return "", out, nil
	}
	object, err := nativeJSONObject(data)
	if err != nil {
		return "", DispatchOutcome{State: DispatchNotAccepted, Reason: "token_response_invalid"}, nil
	}
	code, present, validCode := qqBusinessCode(object)
	if present {
		if !validCode {
			return "", DispatchOutcome{State: DispatchNotAccepted, Reason: "token_response_invalid"}, nil
		}
		if code != 0 {
			priorSuspend := out.Suspend
			out.State, out.Reason, out.APIErrorCode, out.Suspend = DispatchRejected, "token_rejected", code, true
			if code == 100001 {
				out.State = DispatchNotAccepted
				out.Reason = "rate_limited"
				out.Suspend = priorSuspend
			}
			return "", out, nil
		}
	}
	token, valid := officialString(object["access_token"])
	ttl, validTTL := officialDecimal(object["expires_in"], 7200)
	if !valid || !validTTL || len(token) < 1 || len(token) > 8192 || strings.IndexFunc(token, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
		return "", DispatchOutcome{State: DispatchNotAccepted, Reason: "token_response_invalid"}, nil
	}
	s.qqToken, s.qqTokenUntil = token, s.now().Add(time.Duration(ttl)*time.Second)
	return token, DispatchOutcome{}, nil
}

func (s *Official) dispatchQQ(ctx context.Context, frozen officialFrozen) (DispatchOutcome, error) {
	token, out, err := s.qqAccess(ctx)
	if token == "" {
		return out, err
	}
	segment := "users"
	if s.credential.TargetType == "group" {
		segment = "groups"
	}
	request, err := officialJSONRequest(ctx, http.MethodPost, "https://api.bot.qq.com/v2/"+segment+"/"+s.credential.TargetID+"/messages", map[string]any{"msg_type": 0, "content": frozen.Body})
	if err != nil {
		return DispatchOutcome{State: DispatchRejected, Reason: "payload_invalid"}, err
	}
	request.Header.Set("Authorization", "QQBot "+token)
	request.Header.Set("X-Union-Appid", s.credential.AppID)
	status, headers, data, err := s.request(ctx, request)
	out = officialHTTPFailure(status, headers, s.now())
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
	code, present, valid := qqBusinessCode(object)
	if !valid {
		return out, nil
	}
	if present && code != 0 {
		out.APIErrorCode = code
		out.State = DispatchRejected
		out.Reason = "provider_rejected"
		switch code {
		case 40034100:
			out.State = DispatchNotAccepted
			out.Reason = "rate_limited"
			if out.RetryAfterDuration < 3*time.Second {
				out.RetryAfterDuration = 3 * time.Second
			}
		case 40054013:
			out.State = DispatchOptedOut
			out.Reason = "recipient_opted_out"
		case 11243, 11253, 11254, 11265, 40034105:
			out.Reason = "credential_or_permission_rejected"
			out.Suspend = true
		case 40034101, 40054003, 40054004:
			out.Reason = "recipient_unavailable"
			out.Suspend = true
		case 1100300, 50055001, 50055002:
			out.State = DispatchUnknown
			out.Reason = "unconfirmed_provider_failure"
		}
		return out, nil
	}
	if status != 200 {
		return out, nil
	}
	id, idOK := officialString(object["id"])
	timestamp, timestampOK := officialString(object["timestamp"])
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		timestampOK = false
	}
	// The freshly minted token is not part of the static credential snapshot.
	// Compare against this request's immutable local token, never the mutable
	// cache, so concurrent refresh cannot permit a reflected token receipt.
	if !idOK || !officialReceiptID(id) || !timestampOK || strings.Contains(id, token) {
		return out, nil
	}
	return DispatchOutcome{State: DispatchAccepted, ProviderID: id, ProviderState: "api_accepted", Reason: "api_accepted", StatusCode: status}, nil
}

func qqBusinessCode(object map[string]json.RawMessage) (int, bool, bool) {
	code, present := 0, false
	for _, key := range []string{"err_code", "code"} {
		if raw, exists := object[key]; exists {
			value, valid := officialInteger(raw)
			if !valid || present && value != code {
				return 0, true, false
			}
			code, present = value, true
		}
	}
	return code, present, true
}

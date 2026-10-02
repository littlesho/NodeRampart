// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

var twilioMessageSID = regexp.MustCompile(`^SM[0-9a-fA-F]{32}$`)
var twilioPrice = regexp.MustCompile(`^-?[0-9]{1,12}(\.[0-9]{1,12})?$`)
var twilioCurrency = regexp.MustCompile(`^[a-zA-Z]{3}$`)

func (s *Official) twilioAuth(request *http.Request) {
	if s.credential.AuthMode == "api_key" {
		request.SetBasicAuth(s.credential.APIKeySID, s.credential.APIKeySecret)
	} else {
		request.SetBasicAuth(s.credential.AccountSID, s.credential.AuthToken)
	}
}
func (s *Official) twilioBase() string {
	return "https://api.twilio.com/2010-04-01/Accounts/" + s.credential.AccountSID + "/Messages"
}

func (s *Official) dispatchTwilio(ctx context.Context, message store.OutboxMessage, frozen officialFrozen) (DispatchOutcome, error) {
	remaining := message.ExpiresAt.Sub(s.now()) / time.Second
	if message.ExpiresAt.IsZero() || remaining < 1 {
		return DispatchOutcome{State: DispatchRejected, Reason: "message_expired"}, errors.New("Twilio message expiry is invalid")
	}
	if remaining > 300 {
		remaining = 300
	}
	form := url.Values{"To": {s.credential.To}, "Body": {frozen.Body}, "SmartEncoded": {"false"}, "ShortenUrls": {"false"}, "SendAsMms": {"false"}, "ValidityPeriod": {strconv.FormatInt(int64(remaining), 10)}}
	if s.credential.From != "" {
		form.Set("From", s.credential.From)
	} else {
		form.Set("MessagingServiceSid", s.credential.MessagingServiceSID)
	}
	encoded := form.Encode()
	if len(encoded) > 16<<10 {
		return DispatchOutcome{State: DispatchRejected, Reason: "payload_invalid"}, errors.New("Twilio encoded request exceeds its bound")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.twilioBase()+".json", strings.NewReader(encoded))
	if err != nil {
		return DispatchOutcome{State: DispatchRejected, Reason: "payload_invalid"}, errors.New("Twilio request could not be constructed")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "NodeRampart")
	s.twilioAuth(request)
	status, headers, data, err := s.request(ctx, request)
	out := officialHTTPFailure(status, headers, s.now())
	if err != nil {
		return out, err
	}
	if status != 201 {
		return twilioHTTPError(out, data), nil
	}
	receipt, valid := s.twilioReceipt(data, frozen.Body, "")
	if !valid {
		return out, nil
	}
	receipt.StatusCode = status
	return receipt, nil
}

func (s *Official) pollTwilio(ctx context.Context, message store.OutboxMessage) (DispatchOutcome, error) {
	if !twilioMessageSID.MatchString(message.ProviderID) {
		return DispatchOutcome{State: DispatchRejected, Reason: "poll_receipt_invalid"}, errors.New("Twilio poll receipt is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.twilioBase()+"/"+message.ProviderID+".json", nil)
	if err != nil {
		return DispatchOutcome{State: DispatchUnknown, Reason: "poll_request_invalid"}, errors.New("Twilio poll request could not be constructed")
	}
	s.twilioAuth(request)
	request.Header.Set("User-Agent", "NodeRampart")
	status, headers, data, err := s.request(ctx, request)
	out := officialHTTPFailure(status, headers, s.now())
	out.ProviderID = message.ProviderID
	out.ProviderState = "unknown"
	if err != nil {
		return out, err
	}
	if status != 200 {
		out = twilioHTTPError(out, data)
		out.ProviderID = message.ProviderID
		out.ProviderState = "unknown"
		return out, nil
	}
	receipt, valid := s.twilioReceipt(data, message.Body, message.ProviderID)
	if !valid {
		out.State = DispatchUnknown
		out.Reason = "poll_response_invalid"
		return out, nil
	}
	receipt.StatusCode = status
	return receipt, nil
}

func twilioHTTPError(out DispatchOutcome, data []byte) DispatchOutcome {
	if out.StatusCode < 400 || out.StatusCode >= 500 || out.StatusCode == 408 {
		return out
	}
	object, err := nativeJSONObject(data)
	if err != nil {
		return out
	}
	code, valid := officialInteger(object["code"])
	if !valid || code < 1 {
		return out
	}
	out.APIErrorCode = code
	if code == 21610 && out.StatusCode == 400 {
		out.State = DispatchOptedOut
		out.Reason = "recipient_opted_out"
	}
	return out
}

func (s *Official) twilioReceipt(data []byte, body, expectedSID string) (DispatchOutcome, bool) {
	object, err := nativeJSONObject(data)
	if err != nil {
		return DispatchOutcome{}, false
	}
	sid, sidOK := officialString(object["sid"])
	account, accountOK := officialString(object["account_sid"])
	to, toOK := officialString(object["to"])
	echo, bodyOK := officialString(object["body"])
	state, stateOK := officialString(object["status"])
	if !sidOK || !twilioMessageSID.MatchString(sid) || expectedSID != "" && sid != expectedSID || !accountOK || account != s.credential.AccountSID || !toOK || to != s.credential.To || !bodyOK || echo != body || !stateOK {
		return DispatchOutcome{}, false
	}
	out := DispatchOutcome{State: DispatchAccepted, ProviderID: sid, ProviderState: state, Reason: "api_accepted"}
	switch state {
	case "accepted", "queued", "sending":
		out.PollAfterDuration = 30 * time.Second
	case "sent":
		out.Reason = "carrier_accepted"
		out.PollAfterDuration = 5 * time.Minute
	case "delivered":
		out.Reason = "carrier_delivered"
	case "failed", "undelivered", "canceled":
		out.Reason = "delivery_failed"
	default:
		return DispatchOutcome{}, false
	}
	if raw, exists := object["error_code"]; exists && string(raw) != "null" {
		code, valid := officialInteger(raw)
		if !valid || code < 0 {
			return DispatchOutcome{}, false
		}
		out.APIErrorCode = code
		if code == 21610 {
			out.State = DispatchOptedOut
			out.Reason = "recipient_opted_out"
			out.PollAfterDuration = 0
		}
	}
	if raw, exists := object["num_segments"]; exists && string(raw) != "null" {
		value, valid := officialString(raw)
		n, err := strconv.Atoi(value)
		if !valid || err != nil || n < 0 || n > 100 || strconv.Itoa(n) != value {
			return DispatchOutcome{}, false
		}
		out.PlatformSegments = &n
	}
	if raw, exists := object["price"]; exists && string(raw) != "null" {
		value, valid := officialString(raw)
		unit, unitOK := officialString(object["price_unit"])
		if !valid || !twilioPrice.MatchString(value) || !unitOK || !twilioCurrency.MatchString(unit) {
			return DispatchOutcome{}, false
		}
		out.Price = &value
		out.PriceUnit = strings.ToUpper(unit)
	}
	return out, true
}

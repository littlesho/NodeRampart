// SPDX-License-Identifier: MIT

package config

// OfficialCredentialFieldNames is a bounded management allowlist. Values are
// always hidden inputs; this list never contains values from credential files.
func OfficialCredentialFieldNames(channel string) []string {
	switch channel {
	case "qqbot":
		return []string{"app_id", "app_secret", "target_type", "target_id"}
	case "line":
		return []string{"channel_access_token", "target_type", "target_id"}
	case "twilio_sms":
		return []string{"account_sid", "auth_mode", "api_key_sid", "api_key_secret", "auth_token", "from", "messaging_service_sid", "to"}
	case "whatsapp_cloud":
		return []string{"phone_number_id", "access_token", "recipient", "graph_version", "event_name", "event_language", "event_parameters", "daily_name", "daily_language", "daily_parameters", "test_name", "test_language", "test_parameters"}
	}
	return nil
}

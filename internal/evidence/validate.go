// SPDX-License-Identifier: MIT

package evidence

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
)

// Validate rechecks the public DTO before local publication. Receiving an
// authenticated response does not make arbitrary strings safe for sharing.
func (b Bundle) Validate() error {
	bad := errors.New("invalid or oversized evidence bundle")
	if b.FormatVersion != FormatVersion || (b.Kind != "incident" && b.Kind != "diagnostic") || (b.Kind == "incident") != (b.Incident != nil) || !api.ValidTimeRange(b.Start, b.End, 8) || b.SnapshotAt.IsZero() || b.Privacy != PrivacyNotice || b.Consistency != SnapshotNotice || b.Coverage.Qualification != coverageNotice || b.Retention.Qualification != retentionNotice {
		return bad
	}
	if len(b.Events) > MaxRecords || len(b.RelatedSSH) > MaxRecords || len(b.Coverage.Components) > 128 || len(b.Coverage.Segments) > MaxRecords || len(b.Coverage.Gaps) > MaxRecords || len(b.Coverage.LossHours) > MaxRecords || len(b.Retention.Entries) > MaxRecords || len(b.Retention.Totals) > 55 || len(b.Retention.Retained) > 11 || len(b.Monitors) > 32 {
		return bad
	}
	if b.Incident != nil && (b.Incident.Correlation != CorrelationNotice || !validAlias(b.Incident.Alias, "incident")) {
		return bad
	}
	for _, m := range b.Monitors {
		if m.IncidentAlias != "" && !validAlias(m.IncidentAlias, "incident") || m.Milestone != 0 && m.Milestone != 80 && m.Milestone != 100 {
			return bad
		}
	}
	for _, events := range [][]Event{b.Events, b.RelatedSSH} {
		for _, e := range events {
			if e.Alert != nil && e.Alert.Validate(e.Kind) != nil {
				return bad
			}
			if !validAlias(e.Alias, "event") || e.IncidentAlias != "" && !validAlias(e.IncidentAlias, "incident") || e.SourceAlias != "" && !validAlias(e.SourceAlias, "source") || e.Delivery.NotificationAlias != "" && !validAlias(e.Delivery.NotificationAlias, "notification") || e.Delivery.SilenceAlias != "" && !validAlias(e.Delivery.SilenceAlias, "silence") {
				return bad
			}
		}
	}
	if !safeValue(reflect.ValueOf(b), "") {
		return bad
	}
	data, err := json.Marshal(b)
	if err != nil || len(data) > MaxJSONBytes {
		return bad
	}
	return nil
}

func validAlias(value, prefix string) bool {
	if len(value) != len(prefix)+1+32 || !strings.HasPrefix(value, prefix+"_") {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix)+1:])
	return err == nil && value == strings.ToLower(value)
}

func safeValue(v reflect.Value, field string) bool {
	if v.Type() == reflect.TypeFor[model.AlertContext]() {
		context := v.Interface().(model.AlertContext)
		return context.Validate(context.Metric) == nil
	}
	if v.Type() == reflect.TypeFor[time.Time]() {
		t := v.Interface().(time.Time)
		_, offset := t.Zone()
		return t.IsZero() || t.Year() >= 1970 && t.Year() <= 9999 && offset == 0
	}
	switch v.Kind() {
	case reflect.Pointer:
		return v.IsNil() || safeValue(v.Elem(), field)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !safeValue(v.Field(i), v.Type().Field(i).Name) {
				return false
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if !safeValue(v.Index(i), field) {
				return false
			}
		}
	case reflect.Int, reflect.Int64:
		return v.Int() >= 0
	case reflect.String:
		s := v.String()
		if len(s) > 512 {
			return false
		}
		switch field {
		case "Privacy":
			return s == PrivacyNotice
		case "Version":
			return s == safeVersion(s)
		case "Commit":
			return s == safeCommit(s)
		case "RuleFingerprint":
			return s == "" || validHex(s, 64)
		case "RuleFingerprintScope":
			return s == RuleFingerprintNotice
		case "Consistency":
			return s == SnapshotNotice
		case "Correlation":
			return s == CorrelationNotice
		case "Qualification":
			return s == coverageNotice || s == retentionNotice
		case "Alias", "IncidentAlias", "SourceAlias", "NotificationAlias", "SilenceAlias":
			if s == "" {
				return true
			}
			for _, p := range []string{"event", "incident", "source", "notification", "silence"} {
				if validAlias(s, p) {
					return true
				}
			}
			return false
		case "Kind":
			return s == "incident" || s == "diagnostic" || s == kind(s)
		case "Name":
			return s == component(s)
		case "Phase":
			return s == category(s, "observed", "start", "update", "recovery")
		case "Severity":
			return s == category(s, "info", "low", "medium", "high", "critical")
		case "Decision":
			return s == category(s, "legacy", "queued", "merged", "silenced", "ineligible", "rejected")
		case "State":
			return s == category(s, "running", "degraded", "disabled", "conflicting", "sent", "silenced", "ineligible", "rejected", "history_unavailable", "expired", "quarantined", "sending", "pending", "incident_active", "no_active_incident", "milestone_recorded", "no_milestone_recorded")
		case "Dataset":
			return s == dataset(s)
		case "Reason":
			return s == gapReason(s) || s == retentionReason(s)
		case "AggregateSurvives":
			return s == category(s, "totals_preserved", "event_decisions_have_separate_retention", "notification_outcome_preserved", "none_guaranteed")
		case "Key":
			return s == category(s, "budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth", "health_sensor", "health_interface_counter", "health_ssh_journal", "health_storage", "health_geoip_update")
		case "Period":
			return validPeriod(s)
		default:
			return false
		}
	case reflect.Bool, reflect.Uint64:
		return true
	default:
		return false
	}
	return true
}

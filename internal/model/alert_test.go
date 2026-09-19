// SPDX-License-Identifier: MIT

package model

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func alertFields(kind string) map[string]string {
	fields := map[string]string{"reason": "threshold_crossed", "period": "2026-09", "period_start_utc": "2026-09-01T00:00:00Z", "period_end_utc": "2026-10-01T00:00:00Z", "observed_bytes": "100", "threshold_bytes": "100", "observed_cost": "10", "threshold_cost": "10", "currency": "USD", "milestone": "100", "baseline_mean_bytes": "25", "baseline_days": "3", "growth_ratio": "4", "coverage": "incomplete", "basis": LegacyAlertBasis}
	if strings.HasPrefix(kind, "budget_day_") {
		fields["period"], fields["period_start_utc"], fields["period_end_utc"] = "2026-09-11", "2026-09-11T00:00:00Z", "2026-09-12T00:00:00Z"
	}
	if strings.HasPrefix(kind, "health_") {
		fields["reason"], fields["condition_since_utc"] = "component_unavailable", "2026-09-12T00:00:00Z"
	}
	return fields
}

func TestProjectAlertContextPerKindAndLegacyPartial(t *testing.T) {
	for _, kind := range []string{"budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth", "health_sensor", "health_interface_counter", "health_ssh_journal", "health_storage", "health_geoip_update"} {
		t.Run(kind, func(t *testing.T) {
			fields := alertFields(kind)
			got := ProjectAlertContext(kind, fields)
			if err := got.Validate(kind); err != nil || got.Availability != "recorded" {
				t.Fatalf("complete context rejected: %+v, %v", got, err)
			}
			if strings.HasPrefix(kind, "health_") {
				if got.ObservedBytes != nil || got.Period != "" || got.Basis != "" || got.ConditionSince == nil {
					t.Fatal("health context acquired budget fields")
				}
				delete(fields, "condition_since_utc")
			} else {
				if got.Basis != AlertBasis || got.ObservedBytes == nil || *got.ObservedBytes != 100 || got.ConditionSince != nil {
					t.Fatal("budget context lost its recorded input")
				}
				delete(fields, "period_start_utc")
				delete(fields, "period_end_utc")
			}
			legacy := ProjectAlertContext(kind, fields)
			if legacy.Availability != "partial" || legacy.Validate(kind) != nil {
				t.Fatal("incomplete historical context was treated as complete or discarded")
			}
			if got := ProjectAlertContext(kind, nil); got.Availability != "unavailable" || got.Validate(kind) != nil {
				t.Fatal("missing evidence fabricated context")
			}
		})
	}
	if ProjectAlertContext("syn_flood", alertFields("budget_month_bytes")) != nil || ProjectAlertContext("synthetic_private_kind", nil) != nil {
		t.Fatal("non-monitor event acquired an alert context")
	}
}

func TestProjectAlertContextPreservesRecordedZeroAndRejectsMissingValues(t *testing.T) {
	fields := alertFields("budget_month_cost")
	fields["observed_bytes"], fields["observed_cost"] = "0", "0"
	got := ProjectAlertContext("budget_month_cost", fields)
	if got.Availability != "recorded" || got.ObservedBytes == nil || got.ObservedCost == nil || *got.ObservedBytes != 0 || *got.ObservedCost != 0 {
		t.Fatal("recorded zero was lost")
	}
	data, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(data), `"observed_bytes":0`) || !strings.Contains(string(data), `"observed_cost":0`) {
		t.Fatal("zero-valued pointer fields did not survive serialization")
	}
	delete(fields, "observed_bytes")
	delete(fields, "observed_cost")
	got = ProjectAlertContext("budget_month_cost", fields)
	data, err = json.Marshal(got)
	if err != nil || got.Availability != "partial" || got.ObservedBytes != nil || got.ObservedCost != nil || strings.Contains(string(data), `"observed_bytes"`) || strings.Contains(string(data), `"observed_cost"`) {
		t.Fatal("absent observations were fabricated as zero")
	}
}

func TestProjectAlertContextIntegerConversionBounds(t *testing.T) {
	for _, tc := range []struct {
		kind, field string
		allowed     map[string]int
		read        func(*AlertContext) *int
	}{
		{"budget_month_bytes", "milestone", map[string]int{"0": 0, "80": 80, "100": 100}, func(a *AlertContext) *int { return a.Milestone }},
		{"budget_day_growth", "baseline_days", map[string]int{"0": 0, "1": 1, "29": 29, "30": 30}, func(a *AlertContext) *int { return a.BaselineDays }},
	} {
		for _, value := range []string{"", "0", "1", "29", "30", "31", "79", "80", "81", "100", "101", "2147483648", "4294967296", "9223372036854775808", "18446744073709551615", "18446744073709551616", "-1", "+1", "1.5", "invalid"} {
			t.Run(tc.field+"/"+value, func(t *testing.T) {
				fields := alertFields(tc.kind)
				fields[tc.field] = value
				got := ProjectAlertContext(tc.kind, fields)
				converted := tc.read(got)
				want, valid := tc.allowed[value]
				if valid {
					if converted == nil || *converted != want || got.Availability != "recorded" {
						t.Fatal("bounded integer did not survive projection exactly")
					}
				} else if converted != nil || got.Availability != "partial" {
					t.Fatal("out-of-domain integer was converted or fabricated")
				}
				if err := got.Validate(tc.kind); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestProjectAlertContextPreservesFullWidthByteCounters(t *testing.T) {
	fields := alertFields("budget_month_bytes")
	fields["observed_bytes"], fields["threshold_bytes"] = strconv.FormatUint(math.MaxUint64, 10), strconv.FormatUint(math.MaxUint64, 10)
	got := ProjectAlertContext("budget_month_bytes", fields)
	if got.ObservedBytes == nil || *got.ObservedBytes != math.MaxUint64 || got.ThresholdBytes == nil || *got.ThresholdBytes != math.MaxUint64 || got.Availability != "recorded" {
		t.Fatal("64-bit byte counters were narrowed")
	}
	if err := got.Validate("budget_month_bytes"); err != nil {
		t.Fatal(err)
	}
}

func TestProjectAlertContextDropsUntrustedTextAndInvalidNumbers(t *testing.T) {
	cases := []struct{ kind, field, value string }{
		{"budget_month_bytes", "observed_bytes", "18446744073709551616"},
		{"budget_month_bytes", "threshold_bytes", "-1"},
		{"budget_month_bytes", "threshold_bytes", "0"},
		{"budget_month_bytes", "observed_bytes", "+1"},
		{"budget_month_bytes", "observed_bytes", strings.Repeat("1", 10000)},
		{"budget_month_cost", "observed_cost", "NaN"},
		{"budget_month_cost", "observed_cost", "Inf"},
		{"budget_month_cost", "observed_cost", "1e121"},
		{"budget_month_cost", "threshold_cost", "1e13"},
		{"budget_month_cost", "threshold_cost", "0"},
		{"budget_day_growth", "baseline_mean_bytes", "-2"},
		{"budget_day_growth", "baseline_days", "31"},
		{"budget_day_growth", "growth_ratio", "101"},
		{"budget_day_growth", "growth_ratio", "1"},
		{"budget_month_cost", "currency", "synthetic_private_currency"},
		{"budget_month_bytes", "period", "synthetic_private_period"},
		{"budget_month_bytes", "basis", "synthetic_private_basis"},
		{"budget_month_bytes", "coverage", "synthetic_private_coverage"},
		{"budget_month_bytes", "milestone", "81"},
		{"health_storage", "reason", "synthetic_private_reason"},
		{"health_storage", "condition_since_utc", "synthetic_private_time"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.field+"/"+tc.value[:min(len(tc.value), 24)], func(t *testing.T) {
			fields := alertFields(tc.kind)
			fields[tc.field] = tc.value
			fields["token"], fields["profile"], fields["path"], fields["evaluation"] = "synthetic_private_token", "synthetic_private_profile", "synthetic_private_path", "synthetic_private_evaluation"
			got := ProjectAlertContext(tc.kind, fields)
			data, err := json.Marshal(got)
			if err != nil || got.Validate(tc.kind) != nil || got.Availability != "partial" || strings.Contains(string(data), "synthetic_private") {
				t.Fatalf("invalid value escaped fixed projection: %+v, %v", got, err)
			}
		})
	}
	fields := alertFields("budget_month_cost")
	fields["observed_cost"], fields["observed_bytes"] = "1e120", "18446744073709551615"
	if got := ProjectAlertContext("budget_month_cost", fields); got.Availability != "recorded" || got.Validate(got.Metric) != nil {
		t.Fatal("valid bounded numeric extremes rejected")
	}
}

func TestAlertContextValidationRechecksMutatedPublicDTO(t *testing.T) {
	for name, change := range map[string]func(*AlertContext){
		"version":     func(a *AlertContext) { a.SchemaVersion = 2 },
		"metric":      func(a *AlertContext) { a.Metric = "health_storage" },
		"missing":     func(a *AlertContext) { a.ObservedCost = nil },
		"nan":         func(a *AlertContext) { *a.ObservedCost = math.NaN() },
		"infinity":    func(a *AlertContext) { *a.ObservedCost = math.Inf(1) },
		"large cost":  func(a *AlertContext) { *a.ObservedCost = 1e121 },
		"wrong field": func(a *AlertContext) { v := uint64(1); a.ThresholdBytes = &v },
		"reason":      func(a *AlertContext) { a.Reason = "synthetic_private_reason" },
		"currency":    func(a *AlertContext) { a.Currency = "<b>" },
		"availability": func(a *AlertContext) {
			a.Availability = "unavailable"
		},
		"half period": func(a *AlertContext) { a.PeriodEnd = nil },
		"backwards":   func(a *AlertContext) { *a.PeriodEnd = a.PeriodStart.Add(-time.Second) },
		"custom zone": func(a *AlertContext) { *a.PeriodStart = a.PeriodStart.In(time.FixedZone("synthetic_private_zone", 0)) },
	} {
		t.Run(name, func(t *testing.T) {
			got := ProjectAlertContext("budget_month_cost", alertFields("budget_month_cost"))
			change(got)
			if got.Validate("budget_month_cost") == nil {
				t.Fatal("mutated public DTO escaped independent validation")
			}
		})
	}
	health := ProjectAlertContext("health_sensor", alertFields("health_sensor"))
	v := uint64(0)
	health.ObservedBytes = &v
	if health.Validate("health_sensor") == nil {
		t.Fatal("health context accepted budget fields")
	}
}

func TestAlertContextNormalizesRecordedTimesWithoutInventingBounds(t *testing.T) {
	fields := alertFields("budget_day_bytes")
	fields["period_start_utc"], fields["period_end_utc"] = "2026-09-11T08:00:00+08:00", "2026-09-12T08:00:00+08:00"
	got := ProjectAlertContext("budget_day_bytes", fields)
	if got.Availability != "recorded" || got.PeriodStart.Location() != time.UTC || got.PeriodStart.Hour() != 0 || got.Validate(got.Metric) != nil {
		t.Fatal("recorded time failed UTC normalization")
	}
	fields["period_end_utc"] = "2026-09-10T00:00:00Z"
	got = ProjectAlertContext("budget_day_bytes", fields)
	if got.Availability != "partial" || got.PeriodStart != nil || got.PeriodEnd != nil || got.Validate(got.Metric) != nil {
		t.Fatal("invalid interval retained or reconstructed")
	}
}

func TestAlertContextPreservesFixedScheduleFailureReason(t *testing.T) {
	got := ProjectAlertContext("health_geoip_update", map[string]string{"reason": "schedule_failed", "condition_since_utc": "2026-09-12T00:00:00Z"})
	if got.Availability != "recorded" || got.Reason != "schedule_failed" || got.Validate("health_geoip_update") != nil {
		t.Fatal("known separate scheduling failure was lost")
	}
}

func FuzzProjectAlertContext(f *testing.F) {
	for _, kind := range []string{"budget_month_bytes", "budget_month_cost", "budget_day_growth", "health_storage", "ssh_login_success"} {
		data, _ := json.Marshal(alertFields(kind))
		f.Add(kind, data)
	}
	f.Add("health_storage", []byte(`{"reason":"<script>private</script>","condition_since_utc":"invalid"}`))
	f.Fuzz(func(t *testing.T, kind string, data []byte) {
		if len(data) > 64<<10 || len(kind) > 128 {
			return
		}
		var fields map[string]string
		if json.Unmarshal(data, &fields) != nil {
			fields = nil
		}
		got := ProjectAlertContext(kind, fields)
		if got == nil {
			if alertKind(kind) {
				t.Fatal("known monitor kind lost explicit availability")
			}
			return
		}
		if err := got.Validate(kind); err != nil {
			t.Fatal("projector produced an invalid context", err)
		}
		projected, err := json.Marshal(got)
		if err != nil || len(projected) > 4096 {
			t.Fatal("projected scalar context exceeded its byte bound")
		}
	})
}

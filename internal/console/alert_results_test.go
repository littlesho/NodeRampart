// SPDX-License-Identifier: MIT

package console

import (
	"strings"
	"testing"
)

func TestEventAlertResultExplainsPartialRecordedValuesWithoutRounding(t *testing.T) {
	input := `{"timeline":{"events":[{"alert":{"schema_version":1,"availability":"partial","metric":"budget_month_bytes","observed_bytes":9007199254740993,"threshold_bytes":100,"basis":"selected_interface_guest_tx"}}]}}`
	for _, language := range []string{"en", "zh"} {
		got := humanResult(input, language)
		label := "Alert inputs saved with this event"
		qualification := "Partial saved inputs; omitted values are unavailable"
		if language == "zh" {
			label, qualification = "此事件保存的告警依据", "仅有部分事件依据；省略的值不可用"
		}
		if !strings.Contains(got, label) || !strings.Contains(got, qualification) || !strings.Contains(got, "9007199254740993") || strings.Contains(got, "9007199254740992") || strings.Contains(got, "Estimated cost:") || strings.Contains(got, "估算费用:") {
			t.Fatal("historical event context lost qualification, precision, or missing-value distinction")
		}
	}
}

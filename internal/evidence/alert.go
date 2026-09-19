// SPDX-License-Identifier: MIT

package evidence

import "github.com/littlesho/NodeRampart/internal/model"

func copyAlertPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// The authenticated timeline is still an input boundary. A malformed typed
// context becomes explicitly unavailable rather than leaking arbitrary text.
func projectAlert(kind string, value *model.AlertContext) *model.AlertContext {
	if value == nil || value.Validate(kind) != nil {
		return model.ProjectAlertContext(kind, nil)
	}
	copy := *value
	copy.PeriodStart = copyAlertPointer(value.PeriodStart)
	copy.PeriodEnd = copyAlertPointer(value.PeriodEnd)
	copy.ObservedBytes = copyAlertPointer(value.ObservedBytes)
	copy.ThresholdBytes = copyAlertPointer(value.ThresholdBytes)
	copy.ObservedCost = copyAlertPointer(value.ObservedCost)
	copy.ThresholdCost = copyAlertPointer(value.ThresholdCost)
	copy.Milestone = copyAlertPointer(value.Milestone)
	copy.BaselineMeanBytes = copyAlertPointer(value.BaselineMeanBytes)
	copy.BaselineDays = copyAlertPointer(value.BaselineDays)
	copy.GrowthRatio = copyAlertPointer(value.GrowthRatio)
	copy.ConditionSince = copyAlertPointer(value.ConditionSince)
	return &copy
}

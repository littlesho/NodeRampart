// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type assetManagementProbe struct {
	actions    []string
	reconciles int
	err        error
}

func (p *assetManagementProbe) Action(_ context.Context, action string, _ map[string]string) (string, error) {
	p.actions = append(p.actions, action)
	return "updated", p.err
}

func (p *assetManagementProbe) ReconcileGeoSchedule(context.Context) (string, error) {
	p.reconciles++
	return "reconciled", p.err
}

func TestAssetReconcileCommandNeverRunsAnUpdate(t *testing.T) {
	p := &assetManagementProbe{}
	result, err := runAssetManagement(context.Background(), p, "assets-reconcile-schedule")
	if err != nil || result != "reconciled" || p.reconciles != 1 || len(p.actions) != 0 {
		t.Fatal("reconciliation command dispatched a data update")
	}
	p.err = errors.New("synthetic reconciliation failure")
	if _, err := runAssetManagement(context.Background(), p, "assets-reconcile-schedule"); !errors.Is(err, p.err) {
		t.Fatal("reconciliation failure was hidden")
	}
	p.err = nil
	if _, err := runAssetManagement(context.Background(), p, "assets"); err != nil || strings.Join(p.actions, ",") != "geo_refresh" {
		t.Fatal("ordinary update dispatch changed")
	}
}

func TestAssetReconcileRejectsScheduleAndDownloadFlagsBeforeSetup(t *testing.T) {
	for _, args := range [][]string{{"--enabled", "yes"}, {"--language", "zh"}, {"--account-id", "123"}, {"extra"}} {
		err := run(append([]string{"assets", "reconcile-schedule"}, args...))
		if err == nil || err.Error() != "invalid command flags or unexpected positional arguments" {
			t.Fatalf("invalid reconciliation flags reached setup: %v", err)
		}
	}
}

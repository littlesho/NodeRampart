// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/daemon"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestMetricsFailedCollectionReplacesOldHealthyTextfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.prom")
	if err := os.WriteFile(path, []byte("noderampart_collection_success 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := metricsCommand([]string{"export", "--config", filepath.Join(dir, "missing.json"), "--output", path}, &output); err == nil {
		t.Fatal("failed collection reported success")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("noderampart_collection_success 0\n")) || !bytes.Contains(data, []byte("noderampart_export_valid_until_timestamp_seconds")) {
		t.Fatal("old success retained without explicit failure", err, string(data))
	}
	if !strings.Contains(output.String(), `"collection_success": false`) {
		t.Fatal("stdout not structured", output.String())
	}
}

func TestMetricsLabelsAreFixedAndUnknownCountersAreAbsent(t *testing.T) {
	now := time.Now().UTC()
	status := &daemon.Status{GeneratedAt: now, Queue: &store.QueueStatus{}, OptionalFailures: map[string]string{"secret=https://private": "private"}, Readiness: daemon.ReadinessStatus{ConfigurationLoaded: true, ConfigFingerprint: strings.Repeat("a", 64), StorageReady: true, InterfaceReady: true}}
	text := string(prometheusText(status, now))
	if strings.Contains(text, "private") || strings.Contains(text, "https://") || strings.Contains(text, "event_id") || strings.Contains(text, "source_ip") {
		t.Fatal("unbounded labels escaped into metrics", text)
	}
	failed := string(prometheusText(nil, now))
	if strings.Contains(failed, "sensor_batches_total") || !strings.Contains(failed, "health_state 2") {
		t.Fatal("unknown counters represented as zero", failed)
	}
}

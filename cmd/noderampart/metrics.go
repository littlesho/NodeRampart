// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/daemon"
	"github.com/littlesho/NodeRampart/internal/evidence"
)

func metricsCommand(arguments []string, output io.Writer) error {
	if len(arguments) == 0 || arguments[0] != "export" {
		return usageError()
	}
	flags := quietFlags("metrics export")
	path := flags.String("config", defaultConfig, "configuration file")
	target := flags.String("output", "", "caller-owned Prometheus textfile in a private directory")
	manual := flags.Bool("manual-current-uid", false, "explicit manual daemon identity")
	if err := parseFlags(flags, arguments[1:]); err != nil {
		return err
	}
	if *target == "" {
		return errors.New("metrics export requires --output")
	}
	work, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var status *daemon.Status
	cfg, cfgErr := config.Load(*path)
	if cfgErr == nil {
		uid, err := expectedDaemonUID(*manual)
		if err == nil {
			response, err := sendContext(work, cfg.Paths.ControlSocket, api.Request{Version: api.Version, Command: "status"}, uid)
			if err == nil && response.OK {
				data, _ := json.Marshal(response.Data)
				var value daemon.Status
				if json.Unmarshal(data, &value) == nil && value.Diagnosis != nil && value.Diagnosis.Version == 1 &&
					!value.GeneratedAt.IsZero() && time.Since(value.GeneratedAt) <= time.Minute && value.GeneratedAt.Before(time.Now().Add(time.Second)) {
					status = &value
				}
			}
		}
	}
	now := time.Now().UTC()
	// A timed-out collection must still publish an explicit failed marker. Its
	// cleanup/publication budget is independent of the expired request context.
	publish, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := evidence.PublishTextfile(publish, *target, prometheusText(status, now)); err != nil {
		return err
	}
	if err := printJSON(output, map[string]any{"published": true, "collection_success": status != nil, "generated_at_utc": now}); err != nil {
		return err
	}
	if status == nil {
		return errors.New("metrics collection failed; an explicit failed textfile was published")
	}
	return nil
}

func prometheusText(status *daemon.Status, now time.Time) []byte {
	var output bytes.Buffer
	metric := func(name string, value any) { fmt.Fprintf(&output, "noderampart_%s %v\n", name, value) }
	metric("export_generation_timestamp_seconds", now.Unix())
	metric("export_valid_until_timestamp_seconds", now.Add(5*time.Minute).Unix())
	if status == nil {
		metric("collection_success", 0)
		metric("health_state", 2)
		return output.Bytes()
	}
	metric("collection_success", 1)
	metric("status_generation_timestamp_seconds", status.GeneratedAt.Unix())
	metric("health_state", daemon.DiagnoseAt(*status, false, now).Code)
	for _, component := range []struct {
		name           string
		enabled, ready bool
	}{
		{"storage", true, status.Readiness.StorageReady}, {"interface", true, status.Readiness.InterfaceReady},
		{"sensor", status.SensorEnabled, status.Readiness.SensorReady}, {"journal", status.AuthEnabled, status.Readiness.JournalReady}, {"auth", status.AuthEnabled, status.Readiness.AuthDetectionReady},
	} {
		fmt.Fprintf(&output, "noderampart_component_enabled{component=%q} %d\n", component.name, metricBool(component.enabled))
		if component.enabled {
			fmt.Fprintf(&output, "noderampart_component_ready{component=%q} %d\n", component.name, metricBool(component.ready))
		}
	}
	metric("sensor_batches_total", status.Batches)
	metric("event_pending", status.EventIngest.Pending)
	metric("event_dropped_total", status.EventIngest.Dropped)
	metric("auth_capacity_refusals_total", status.AuthDetection.RejectedFailures)
	metric("scan_ignored_packets_total", status.Detection.ScanIgnoredPackets)
	if status.Queue != nil {
		metric("notification_pending", status.Queue.Pending)
		metric("notification_isolated", status.Queue.Isolated)
	}
	return output.Bytes()
}

func metricBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

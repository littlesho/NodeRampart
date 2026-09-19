// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/evidence"
)

type evidenceOptions struct {
	Config, Output, Format string
	Manual                 bool
	Query                  api.EvidenceArgs
}

func parseEvidence(arguments []string, now time.Time) (evidenceOptions, error) {
	var options evidenceOptions
	flags := quietFlags("evidence export")
	flags.StringVar(&options.Config, "config", defaultConfig, "configuration file")
	flags.StringVar(&options.Output, "output", "", "new file in a private directory")
	flags.StringVar(&options.Format, "format", "zip", "zip, html or json")
	flags.BoolVar(&options.Manual, "manual-current-uid", false, "explicit manual lab daemon identity")
	flags.StringVar(&options.Query.IncidentID, "incident", "", "optional incident ID; blank exports diagnostic history")
	var start, end string
	flags.StringVar(&start, "since", "", "inclusive RFC3339 start, up to eight days before until")
	flags.StringVar(&end, "until", now.UTC().Format(time.RFC3339Nano), "exclusive RFC3339 end")
	if err := parseFlags(flags, arguments); err != nil {
		return options, err
	}
	invalid := errors.New("evidence export requires a valid period, zip/html/json format and a new output file")
	to, e2 := time.Parse(time.RFC3339Nano, end)
	if start == "" {
		duration := 24 * time.Hour
		if options.Query.IncidentID != "" {
			duration = 7 * 24 * time.Hour
		}
		start = to.Add(-duration).UTC().Format(time.RFC3339Nano)
	}
	from, e1 := time.Parse(time.RFC3339Nano, start)
	options.Query.Start, options.Query.End = from.UTC(), to.UTC()
	if e1 != nil || e2 != nil || options.Query.Validate() != nil || options.Output == "" || len(options.Output) > 4096 ||
		strings.IndexFunc(options.Output, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 ||
		(options.Format != "zip" && options.Format != "html" && options.Format != "json") {
		return options, invalid
	}
	return options, nil
}

func evidenceCommand(arguments []string, output io.Writer) error {
	options, err := parseEvidence(arguments, time.Now().UTC())
	if err != nil {
		return err
	}
	cfg, err := config.Load(options.Config)
	if err != nil {
		return errors.New("configuration is unavailable or invalid")
	}
	uid, err := expectedDaemonUID(options.Manual)
	if err != nil {
		return err
	}
	args, err := json.Marshal(options.Query)
	if err != nil {
		return errors.New("evidence query could not be encoded")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	response, err := sendContext(ctx, cfg.Paths.ControlSocket, api.Request{Version: api.Version, Command: "evidence_snapshot", Args: args}, uid)
	if err != nil || !response.OK {
		return errors.New("authenticated evidence snapshot is unavailable")
	}
	data, ok := response.Data.(json.RawMessage)
	if !ok || len(data) > evidence.MaxJSONBytes {
		return errors.New("evidence response exceeds its format or size limit")
	}
	var bundle evidence.Bundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&bundle) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("evidence response is invalid")
	}
	if err := evidence.Export(ctx, bundle, options.Output, options.Format); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "Local redacted evidence saved. UTC timing and counts can remain linkable; review the artifact before sharing. No upload or notification was sent.")
	return err
}

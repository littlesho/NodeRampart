// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/littlesho/NodeRampart/internal/manage"
)

func thresholdCommand(arguments []string, output io.Writer) error {
	if len(arguments) == 0 {
		return errors.New("usage: threshold preview|feedback [flags]")
	}
	flags := quietFlags("threshold")
	var input, current, candidate, comparison, file, side, event, label string
	switch arguments[0] {
	case "preview":
		flags.StringVar(&input, "input", "", "bounded offline JSONL replay metadata")
		flags.StringVar(&current, "current", "", "current local configuration")
		flags.StringVar(&candidate, "candidate", "", "candidate local configuration")
	case "feedback":
		flags.StringVar(&comparison, "comparison", "", "saved preview or replay comparison JSON")
		flags.StringVar(&file, "file", "", "protected local feedback JSON file")
		flags.StringVar(&side, "side", "candidate", "baseline or candidate")
		flags.StringVar(&event, "event", "", "retained evt_ID")
		flags.StringVar(&label, "label", "", "reasonable, false_positive or uncertain")
	default:
		return errors.New("usage: threshold preview|feedback [flags]")
	}
	if err := parseFlags(flags, arguments[1:]); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if arguments[0] == "preview" {
		if input == "" || current == "" || candidate == "" {
			return errors.New("preview requires input, current and candidate files")
		}
		result, err := manage.PreviewThresholdFiles(ctx, input, current, candidate)
		if err != nil {
			return err
		}
		return printJSON(output, result)
	}
	if comparison == "" || file == "" || event == "" || label == "" {
		return errors.New("feedback requires comparison, file, event and label")
	}
	result, err := manage.RecordThresholdFeedback(ctx, comparison, file, side, event, label)
	if err != nil {
		return err
	}
	return printJSON(output, result)
}

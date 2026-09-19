// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/littlesho/NodeRampart/internal/replay"
)

func replayCommand(arguments []string, output io.Writer) error {
	if len(arguments) < 1 {
		return usageError()
	}
	flags := quietFlags("replay")
	input := flags.String("input", "", "bounded JSONL metadata file")
	var destination, baseline, candidate string
	switch arguments[0] {
	case "anonymize":
		flags.StringVar(&destination, "output", "", "new pseudonymized JSONL output file")
	case "compare":
		flags.StringVar(&baseline, "baseline", "", "baseline configuration")
		flags.StringVar(&candidate, "candidate", "", "candidate configuration")
	default:
		return usageError()
	}
	if err := parseFlags(flags, arguments[1:]); err != nil {
		return err
	}
	if *input == "" || arguments[0] == "anonymize" && destination == "" || arguments[0] == "compare" && (baseline == "" || candidate == "") {
		return errors.New("replay requires input and output or both threshold configurations")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if arguments[0] == "anonymize" {
		result, err := replay.Anonymize(ctx, *input, destination)
		if err != nil {
			return err
		}
		return printJSON(output, result)
	}
	a, err := replay.LoadRules(baseline)
	if err != nil {
		return err
	}
	b, err := replay.LoadRules(candidate)
	if err != nil {
		return err
	}
	result, err := replay.Compare(ctx, *input, a, b)
	if err != nil {
		return err
	}
	return printJSON(output, result)
}

// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
	"github.com/littlesho/NodeRampart/internal/version"
)

const defaultConfig = "/etc/noderampart/config.json"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "noderampart:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		if stdinTerminal() {
			return managementCommand("tui", nil)
		}
		return usageError()
	}
	switch arguments[0] {
	case "tui", "setup":
		return managementCommand(arguments[0], arguments[1:])
	case "assets":
		if len(arguments) < 2 || arguments[1] != "update" {
			return usageError()
		}
		return managementCommand("assets", arguments[2:])
	case "version":
		if len(arguments) != 1 {
			return usageError()
		}
		return printJSON(os.Stdout, version.Current())
	case "config":
		if len(arguments) < 2 || arguments[1] != "test" {
			return usageError()
		}
		flags := quietFlags("config test")
		path := flags.String("config", defaultConfig, "configuration file")
		if err := parseFlags(flags, arguments[2:]); err != nil {
			return err
		}
		cfg, err := config.Load(*path)
		if err != nil {
			return errors.New("configuration could not be loaded or is invalid")
		}
		fmt.Printf("configuration is valid (schema %d)\n", cfg.SchemaVersion)
		return nil
	case "doctor":
		return doctorCommand(arguments[1:], os.Stdout)
	case "status":
		return command(arguments[1:], "status")
	case "health":
		return command(arguments[1:], "health")
	case "retention":
		return command(arguments[1:], "retention")
	case "alerts":
		if len(arguments) < 2 || arguments[1] != "status" {
			return usageError()
		}
		return command(arguments[2:], "alerts_status")
	case "evidence":
		if len(arguments) < 2 || arguments[1] != "export" {
			return usageError()
		}
		return evidenceCommand(arguments[2:], os.Stdout)
	case "replay":
		return replayCommand(arguments[1:], os.Stdout)
	case "events", "incident", "report", "notify", "backup":
		if len(arguments) < 2 {
			return usageError()
		}
		if arguments[0] == "notify" && arguments[1] == "silence" {
			if len(arguments) < 3 {
				return usageError()
			}
			name, ok := map[string]string{"add": "notify_silence_add", "list": "notify_silence_list", "remove": "notify_silence_remove"}[arguments[2]]
			if !ok {
				return usageError()
			}
			return command(arguments[3:], name)
		}
		commands := map[string]map[string]string{
			"events":   {"list": "events_list", "show": "events_show", "timeline": "events_timeline"},
			"incident": {"list": "incident_list", "show": "incident_show"},
			"report":   {"now": "report_now", "list": "report_list", "show": "report_show", "backfill": "report_backfill"},
			"notify":   {"test": "notify_test", "status": "notify_status", "list": "notify_list", "retry": "notify_retry", "quarantine": "notify_quarantine", "resume": "notify_resume", "resume-destination": "notify_resume"},
			"backup":   {"create": "backup_create", "verify": "backup_verify", "restore": "backup_restore"},
		}
		name, ok := commands[arguments[0]][arguments[1]]
		if !ok {
			return usageError()
		}
		if name == "backup_verify" || name == "backup_restore" {
			return offlineBackupCommand(arguments[2:], name, os.Stdout)
		}
		return command(arguments[2:], name)
	default:
		return usageError()
	}
}

type commandOptions struct {
	Config  string
	Manual  bool
	Format  string
	Request api.Request
}

func quietFlags(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func parseFlags(flags *flag.FlagSet, arguments []string) error {
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return errors.New("invalid command flags or unexpected positional arguments")
	}
	return nil
}

func parseCommand(arguments []string, name string, now time.Time) (commandOptions, error) {
	options := commandOptions{Request: api.Request{Version: api.Version, Command: name}}
	flags := quietFlags(name)
	flags.StringVar(&options.Config, "config", defaultConfig, "configuration file")
	flags.BoolVar(&options.Manual, "manual-current-uid", false, "explicitly trust a daemon running as the current UID for a manual lab")
	var id, before, kind, start, end, date, destination, output string
	var incident, cursorTime, cursorID, expires, reason string
	var limit int
	var offset int
	var retentionBefore int64
	var dataset string
	switch name {
	case "retention":
		flags.StringVar(&start, "since", now.Add(-7*24*time.Hour).UTC().Format(time.RFC3339Nano), "inclusive affected-data period start; maximum 400 days")
		flags.StringVar(&end, "until", now.UTC().Format(time.RFC3339Nano), "exclusive affected-data period end")
		flags.StringVar(&dataset, "dataset", "", "optional dataset from the retention result")
		flags.StringVar(&reason, "reason", "", "optional fixed retention reason")
		flags.Int64Var(&retentionBefore, "before-id", 0, "next_before_id from the previous page")
		flags.IntVar(&limit, "limit", 20, "1..100 retained ledger entries")
	case "report_backfill":
		flags.StringVar(&start, "from", "", "first completed date YYYY-MM-DD")
		flags.StringVar(&end, "through", "", "last completed date YYYY-MM-DD; maximum 31 dates")
	case "health":
		flags.StringVar(&start, "since", now.Add(-24*time.Hour).UTC().Format(time.RFC3339Nano), "inclusive UTC period start, maximum 400 days")
		flags.StringVar(&end, "until", now.UTC().Format(time.RFC3339Nano), "exclusive UTC period end")
		flags.IntVar(&limit, "limit", 100, "1..100 coverage segments")
		flags.IntVar(&offset, "offset", 0, "bounded coverage segment continuation offset")
	case "events_timeline", "incident_show", "incident_list":
		flags.StringVar(&start, "since", now.Add(-7*24*time.Hour).UTC().Format(time.RFC3339Nano), "inclusive RFC3339 start; range up to eight days")
		flags.StringVar(&end, "until", now.UTC().Format(time.RFC3339Nano), "exclusive RFC3339 end; keep fixed when paging")
		flags.IntVar(&limit, "limit", 20, "1..100 records")
		if name == "incident_list" {
			flags.StringVar(&cursorTime, "before", "", "previous page's last timestamp")
			flags.StringVar(&cursorID, "before-id", "", "previous page's last incident ID")
		} else {
			flags.StringVar(&cursorTime, "after", "", "previous page's last timestamp")
			flags.StringVar(&cursorID, "after-id", "", "previous page's last event ID")
			if name == "incident_show" {
				flags.StringVar(&incident, "id", "", "incident ID")
			} else {
				flags.StringVar(&incident, "incident", "", "optional incident ID")
				flags.StringVar(&kind, "kind", "", "event kind")
			}
		}
	case "notify_silence_add":
		flags.StringVar(&incident, "incident", "", "incident selector")
		flags.StringVar(&kind, "kind", "", "event kind selector; combined with incident when both are set")
		flags.StringVar(&expires, "until", "", "explicit RFC3339 expiry within seven days")
		flags.StringVar(&reason, "reason", "", "optional plain-text reason, up to 256 bytes")
	case "events_list":
		flags.StringVar(&start, "since", now.Add(-7*24*time.Hour).Format(time.RFC3339Nano), "inclusive RFC3339 start")
		flags.StringVar(&end, "until", now.Format(time.RFC3339Nano), "exclusive RFC3339 end; use last timestamp with --before-id")
		flags.StringVar(&before, "before-id", "", "last event ID from previous page")
		flags.StringVar(&kind, "kind", "", "event kind")
		flags.IntVar(&limit, "limit", 20, "1..100 events")
	case "events_show", "notify_retry", "notify_quarantine", "notify_silence_remove":
		flags.StringVar(&id, "id", "", "record ID")
	case "report_list", "notify_list":
		flags.StringVar(&before, "before", "", "exclusive previous page date or message ID")
		flags.IntVar(&limit, "limit", 20, "1..100 records")
	case "report_show":
		flags.StringVar(&date, "date", "", "report date YYYY-MM-DD")
		flags.StringVar(&options.Format, "format", "text", "json, text, or html")
	case "report_now":
		flags.StringVar(&options.Format, "format", "text", "json, text, or html")
	case "notify_resume":
		flags.StringVar(&destination, "destination", "", "destination to resume")
	case "backup_create":
		flags.StringVar(&output, "output", "", "new backup path under the daemon state backups directory")
	case "status", "alerts_status", "notify_status", "notify_test", "notify_silence_list":
	default:
		return options, usageError()
	}
	if err := parseFlags(flags, arguments); err != nil {
		return options, err
	}
	var args any = struct{}{}
	invalid := errors.New("invalid or missing command argument")
	switch name {
	case "retention":
		from, e1 := time.Parse(time.RFC3339Nano, start)
		to, e2 := time.Parse(time.RFC3339Nano, end)
		query := store.RetentionQuery{Start: from.UTC(), End: to.UTC(), Dataset: dataset, Reason: reason, BeforeID: retentionBefore, Limit: limit}
		if e1 != nil || e2 != nil || query.Validate() != nil {
			return options, invalid
		}
		args = query
	case "report_backfill":
		if !api.ValidDate(start) || !api.ValidDate(end) || start > end {
			return options, invalid
		}
		first, _ := time.Parse(time.DateOnly, start)
		last, _ := time.Parse(time.DateOnly, end)
		if last.Sub(first) >= 31*24*time.Hour {
			return options, invalid
		}
		args = api.BackfillArgs{From: start, Through: end}
	case "health":
		from, e1 := time.Parse(time.RFC3339Nano, start)
		to, e2 := time.Parse(time.RFC3339Nano, end)
		if e1 != nil || e2 != nil || !api.ValidTimeRange(from, to, 400) || !api.ValidLimit(limit) || offset < 0 || offset > 20256 {
			return options, invalid
		}
		args = api.HealthArgs{Start: from.UTC(), End: to.UTC(), Limit: limit, Offset: offset}
	case "events_timeline", "incident_show", "incident_list":
		from, e1 := time.Parse(time.RFC3339Nano, start)
		to, e2 := time.Parse(time.RFC3339Nano, end)
		var cursor time.Time
		if cursorTime != "" {
			var err error
			cursor, err = time.Parse(time.RFC3339Nano, cursorTime)
			if err != nil {
				return options, invalid
			}
		}
		if e1 != nil || e2 != nil || !api.ValidTimeRange(from, to, 8) || !api.ValidLimit(limit) ||
			(cursorID == "") != cursor.IsZero() || cursorID != "" && (!api.ValidID(cursorID) || cursor.Before(from) || !cursor.Before(to)) ||
			incident != "" && !api.ValidID(incident) || kind != "" && !api.ValidID(kind) || name == "incident_show" && incident == "" {
			return options, invalid
		}
		if name == "incident_list" {
			args = api.IncidentListArgs{Start: from.UTC(), End: to.UTC(), BeforeTime: cursor.UTC(), BeforeID: cursorID, Limit: limit}
		} else {
			args = api.TimelineArgs{Start: from.UTC(), End: to.UTC(), AfterTime: cursor.UTC(), AfterID: cursorID, IncidentID: incident, Kind: kind, Limit: limit}
		}
	case "notify_silence_add":
		until, err := time.Parse(time.RFC3339Nano, expires)
		if err != nil || !until.After(now) || until.Sub(now) > 7*24*time.Hour || incident == "" && kind == "" ||
			incident != "" && !api.ValidID(incident) || kind != "" && !api.ValidID(kind) || len(reason) > 256 || strings.IndexFunc(reason, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return options, invalid
		}
		args = api.SilenceArgs{IncidentID: incident, Kind: kind, ExpiresAt: until.UTC(), Reason: reason}
	case "events_list":
		from, e1 := time.Parse(time.RFC3339Nano, start)
		to, e2 := time.Parse(time.RFC3339Nano, end)
		if e1 != nil || e2 != nil || from.IsZero() || to.IsZero() || from.After(to) || !api.ValidLimit(limit) || (before != "" && !api.ValidID(before)) || (kind != "" && !api.ValidID(kind)) {
			return options, invalid
		}
		args = api.EventListArgs{Start: from.UTC(), End: to.UTC(), BeforeID: before, Kind: kind, Limit: limit}
	case "events_show", "notify_retry", "notify_quarantine", "notify_silence_remove":
		if !api.ValidID(id) {
			return options, invalid
		}
		args = api.IDArgs{ID: id}
	case "report_list", "notify_list":
		if !api.ValidLimit(limit) || (before != "" && ((name == "report_list" && !api.ValidDate(before)) || (name == "notify_list" && !api.ValidID(before)))) {
			return options, invalid
		}
		args = api.ListArgs{Before: before, Limit: limit}
	case "report_show":
		if !api.ValidDate(date) {
			return options, invalid
		}
		args = api.DateArgs{Date: date}
	case "notify_resume":
		if !api.ValidID(destination) {
			return options, invalid
		}
		args = api.DestinationArgs{Destination: destination}
	case "backup_create":
		if !cleanLocalPath(output) {
			return options, invalid
		}
		args = api.BackupArgs{Output: output}
	}
	if options.Format != "" && options.Format != "json" && options.Format != "text" && options.Format != "html" {
		return options, errors.New("format must be json, text, or html")
	}
	data, err := json.Marshal(args)
	if err != nil {
		return options, errors.New("could not encode command arguments")
	}
	options.Request.Args = data
	return options, nil
}

func command(arguments []string, name string) error {
	options, err := parseCommand(arguments, name, time.Now().UTC())
	if err != nil {
		return err
	}
	cfg, err := config.Load(options.Config)
	if err != nil {
		return errors.New("configuration could not be loaded or is invalid; run doctor for local diagnostics")
	}
	uid, err := expectedDaemonUID(options.Manual)
	if err != nil {
		return err
	}
	response, err := send(cfg.Paths.ControlSocket, options.Request, uid)
	if err != nil {
		return err
	}
	if !response.OK {
		if name == "report_backfill" && response.Data != nil {
			if err := writeResponse(os.Stdout, options, response.Data); err != nil {
				return err
			}
		}
		return errors.New(response.Error)
	}
	return writeResponse(os.Stdout, options, response.Data)
}

func expectedDaemonUID(manual bool) (uint32, error) {
	if manual {
		return uint32(os.Geteuid()), nil
	}
	return ipc.ServiceUID(ipc.DaemonUser)
}

func send(path string, request api.Request, expectedUID uint32) (api.Response, error) {
	return sendContext(context.Background(), path, request, expectedUID)
}

func sendContext(ctx context.Context, path string, request api.Request, expectedUID uint32) (api.Response, error) {
	if err := ctx.Err(); err != nil {
		return api.Response{}, err
	}
	connection, err := ipc.DialUnixPeer(path, 3*time.Second, expectedUID)
	if err != nil {
		return api.Response{}, err
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	timeout := 15 * time.Second
	if request.Command == "backup_create" {
		timeout = 2*time.Minute + 5*time.Second
	}
	deadline := time.Now().Add(timeout)
	if outer, ok := ctx.Deadline(); ok && outer.Before(deadline) {
		deadline = outer
	}
	_ = connection.SetDeadline(deadline)
	if err := protocol.WriteFrame(connection, request); err != nil {
		return api.Response{}, errors.New("could not send daemon request")
	}
	return readResponse(bufio.NewReader(connection))
}

func readResponse(reader *bufio.Reader) (api.Response, error) {
	// Keep counters as exact JSON integers instead of passing through float64.
	var wire struct {
		Version int             `json:"version"`
		OK      bool            `json:"ok"`
		Error   string          `json:"error,omitempty"`
		Data    json.RawMessage `json:"data,omitempty"`
	}
	if err := protocol.ReadFrame(reader, &wire); err != nil {
		return api.Response{}, errors.New("daemon response is unavailable or invalid")
	}
	if wire.Version != api.Version {
		return api.Response{}, errors.New("daemon API version mismatch")
	}
	return api.Response{Version: wire.Version, OK: wire.OK, Error: wire.Error, Data: wire.Data}, nil
}

func printJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func usageError() error {
	return errors.New("usage: noderampart {tui|setup|assets update|version|config test|doctor|status|health|alerts status|retention|evidence export|events list/show/timeline|incident list/show|report now/list/show/backfill|replay anonymize/compare|notify test/status/list/retry/quarantine/resume|notify silence add/list/remove|backup create/verify/restore} [flags]")
}

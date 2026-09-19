// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"path/filepath"
	"strings"
	"unicode"
)

func cleanLocalPath(value string) bool {
	return len(value) > 0 && len(value) <= 4096 && filepath.IsAbs(value) && filepath.Clean(value) == value && strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

func writeResponse(output io.Writer, options commandOptions, data any) error {
	if options.Format == "" || options.Format == "json" {
		return printJSON(output, data)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return errors.New("could not encode report")
	}
	var report struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Report string `json:"report"`
	}
	if json.Unmarshal(encoded, &report) != nil {
		return errors.New("invalid report response")
	}
	if report.Body == "" {
		report.Body = report.Report
	}
	if report.Body == "" {
		return errors.New("report body is missing")
	}
	if report.Title == "" {
		report.Title = "NodeRampart report"
	}
	plain := plainReport(report.Body)
	if options.Format == "text" {
		_, err = fmt.Fprintln(output, plain)
		return err
	}
	_, err = fmt.Fprintf(output, "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; style-src 'unsafe-inline'\"><title>%s</title><style>body{font:16px/1.5 system-ui;max-width:72rem;margin:2rem auto;padding:0 1rem}pre{white-space:pre-wrap;overflow-wrap:anywhere}</style></head><body><h1>%s</h1><pre>%s</pre></body></html>\n", html.EscapeString(report.Title), html.EscapeString(report.Title), html.EscapeString(plain))
	return err
}

func plainReport(body string) string {
	plain := html.UnescapeString(strings.NewReplacer("<b>", "", "</b>", "").Replace(body))
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, plain)
}

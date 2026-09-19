// SPDX-License-Identifier: MIT

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const MaxArgsSize = 4096

type EventListArgs struct {
	Start    time.Time `json:"start_utc"`
	End      time.Time `json:"end_utc"`
	BeforeID string    `json:"before_id,omitempty"`
	Kind     string    `json:"kind,omitempty"`
	Limit    int       `json:"limit"`
}

type ListArgs struct {
	Before string `json:"before,omitempty"`
	Limit  int    `json:"limit"`
}

type IDArgs struct {
	ID string `json:"id"`
}
type DateArgs struct {
	Date string `json:"date"`
}
type DestinationArgs struct {
	Destination string `json:"destination"`
}
type BackupArgs struct {
	Output string `json:"output"`
}

// DecodeArgs rejects unknown fields, trailing documents and non-object values.
// Its errors never include attacker-controlled JSON or field names.
func DecodeArgs(data json.RawMessage, target any) error {
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	data = bytes.TrimSpace(data)
	if len(data) > MaxArgsSize || len(data) < 2 || data[0] != '{' || data[len(data)-1] != '}' {
		return errors.New("invalid command arguments")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid command arguments")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid command arguments")
	}
	return nil
}

func ValidID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == ':' || r == '.') {
			return false
		}
	}
	return true
}

func ValidDate(value string) bool {
	date, err := time.Parse("2006-01-02", value)
	return err == nil && date.Format("2006-01-02") == value
}

func ValidLimit(value int) bool { return value >= 1 && value <= 100 }

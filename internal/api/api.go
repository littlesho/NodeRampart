// SPDX-License-Identifier: MIT

package api

import "encoding/json"

const Version = 1

type Request struct {
	Version int             `json:"version"`
	Command string          `json:"command"`
	Args    json.RawMessage `json:"args,omitempty"`
}

type Response struct {
	Version int    `json:"version"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Data    any    `json:"data,omitempty"`
}

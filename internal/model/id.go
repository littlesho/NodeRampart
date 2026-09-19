// SPDX-License-Identifier: MIT

package model

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func NewID(prefix string) string {
	var random [10]byte
	if _, err := rand.Read(random[:]); err != nil {
		return prefix + "_" + time.Now().UTC().Format("20060102T150405.000000000")
	}
	return prefix + "_" + hex.EncodeToString(random[:])
}

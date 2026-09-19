// SPDX-License-Identifier: MIT

package evidence

import (
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/littlesho/NodeRampart/internal/version"
)

var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$`)

func safeVersion(v string) string {
	if v == "dev" || v == "unknown" || len(v) <= 64 && releaseVersion.MatchString(v) {
		return v
	}
	return "unknown"
}

func validHex(v string, n int) bool {
	if len(v) != n || v != strings.ToLower(v) {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}

func safeCommit(v string) string {
	if validHex(v, 40) || validHex(v, 64) {
		return v
	}
	return "unknown"
}

func producer() Producer {
	v := version.Current()
	return Producer{Version: safeVersion(v.Version), Commit: safeCommit(v.Commit)}
}

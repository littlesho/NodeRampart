// SPDX-License-Identifier: MIT

package collector

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type AuthKind string

const (
	AuthFailure     AuthKind = "failure"
	AuthSuccess     AuthKind = "success"
	AuthInvalidUser AuthKind = "invalid_user"
	AuthPAMFailure  AuthKind = "pam_failure"
)

type AuthObservation struct {
	ObservedAt  time.Time
	Kind        AuthKind
	SourceIP    netip.Addr
	SourcePort  uint16
	User        string
	Method      string
	Root        bool
	InvalidUser bool
}

const keyDetailsGrammar = `: [A-Za-z0-9_-]+ (?:SHA256:[A-Za-z0-9+/=]+|MD5:[0-9a-fA-F:]+)(?: ID .* \(serial [0-9]+\) CA [A-Za-z0-9_-]+ (?:SHA256:[A-Za-z0-9+/=]+|MD5:[0-9a-fA-F:]+))?(?:, (?:[a-z-]+(?:=[A-Za-z0-9_-]+)?)(?: [a-z-]+(?:=[A-Za-z0-9_-]+)?)*)?`

var (
	// Usernames can contain spaces and text resembling entire log records. The
	// greedy username and anchored endpoint must be kept together: anchoring
	// only the beginning still allows a username to manufacture a source IP.
	sshOutcome  = regexp.MustCompile(`^(Failed|Accepted) (password|publickey|keyboard-interactive(?:/pam)?|hostbased|gssapi-with-mic) for (.*) from ([0-9A-Fa-f:.]+) port ([0-9]+) ssh2((?:` + keyDetailsGrammar + `)?)(?: \[preauth\])?$`)
	invalidUser = regexp.MustCompile(`^Invalid user (.*) from ([0-9A-Fa-f:.]+) port ([0-9]+)(?: ssh2)?(?: \[preauth\])?$`)
	pamFailure  = regexp.MustCompile(`^pam_unix\(sshd:auth\): authentication failure; logname=[^ ]* uid=[0-9]+ euid=[0-9]+ tty=ssh ruser=[^ ]* rhost=([0-9A-Fa-f:.]+)(?: +user=(.*))?$`)
	keyDetails  = regexp.MustCompile(`^` + keyDetailsGrammar + `$`)
	sshEndpoint = regexp.MustCompile(` from [0-9A-Fa-f:.]+ port [0-9]+ ssh2`)
)

func ParseSSH(message string, observedAt time.Time) (AuthObservation, bool) {
	// Truncation could discard the real endpoint and expose a forged one in
	// the username. OpenSSH escapes control characters before logging them.
	if len(message) > 16*1024 || !utf8.ValidString(message) || strings.IndexFunc(message, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return AuthObservation{}, false
	}
	if match := sshOutcome.FindStringSubmatch(message); match != nil {
		kind := AuthFailure
		if match[1] == "Accepted" {
			kind = AuthSuccess
		}
		user := match[3]
		invalid := kind == AuthFailure && strings.HasPrefix(user, "invalid user ")
		if invalid {
			user = strings.TrimPrefix(user, "invalid user ")
		}
		details := strings.TrimSuffix(match[6], " [preauth]")
		if details != "" && ((match[2] != "publickey" && match[2] != "hostbased") || !keyDetails.MatchString(details)) {
			return AuthObservation{}, false
		}
		// A certificate ID is also untrusted text. Refuse ambiguous records
		// instead of treating an endpoint embedded in its suffix as the peer.
		if match[2] == "publickey" || match[2] == "hostbased" {
			candidates := 0
			for _, endpoint := range sshEndpoint.FindAllStringIndex(message, -1) {
				suffix := strings.TrimSuffix(message[endpoint[1]:], " [preauth]")
				if suffix == "" || keyDetails.MatchString(suffix) {
					candidates++
					if candidates > 1 {
						return AuthObservation{}, false
					}
				}
			}
		}
		observation, ok := authEndpoint(observedAt, kind, match[4], match[5], user, match[2])
		observation.InvalidUser = invalid
		return observation, ok
	}
	if match := invalidUser.FindStringSubmatch(message); match != nil {
		observation, ok := authEndpoint(observedAt, AuthInvalidUser, match[2], match[3], match[1], "unknown")
		observation.InvalidUser = true
		return observation, ok
	}
	if match := pamFailure.FindStringSubmatch(message); match != nil {
		user := "unknown"
		if len(match) > 2 && match[2] != "" {
			user = match[2]
		}
		return authObservation(observedAt, AuthPAMFailure, match[1], user, "pam")
	}
	return AuthObservation{}, false
}

func authEndpoint(at time.Time, kind AuthKind, source, port, user, method string) (AuthObservation, bool) {
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return AuthObservation{}, false
	}
	observation, ok := authObservation(at, kind, source, user, method)
	observation.SourcePort = uint16(number)
	return observation, ok
}

func authObservation(at time.Time, kind AuthKind, source, user, method string) (AuthObservation, bool) {
	address, err := netip.ParseAddr(strings.Trim(source, "[]"))
	if err != nil {
		return AuthObservation{}, false
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	root := user == "root"
	user = cleanField(user, 64)
	method = cleanField(method, 32)
	return AuthObservation{ObservedAt: at.UTC(), Kind: kind, SourceIP: address.Unmap(), User: user, Method: method, Root: root}, true
}

func cleanField(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

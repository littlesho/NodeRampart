// SPDX-License-Identifier: MIT

package collector

import (
	"strings"
	"testing"
	"time"
)

func TestParseSSH(t *testing.T) {
	now := time.Now()
	tests := []struct {
		line, ip, user string
		kind           AuthKind
	}{
		{"Failed password for invalid user admin from 203.0.113.5 port 4444 ssh2", "203.0.113.5", "admin", AuthFailure},
		{"Invalid user oracle from 2001:db8::4 port 123 ssh2", "2001:db8::4", "oracle", AuthInvalidUser},
		{"Accepted publickey for root from 192.0.2.9 port 555 ssh2", "192.0.2.9", "root", AuthSuccess},
	}
	for _, test := range tests {
		got, ok := ParseSSH(test.line, now)
		if !ok || got.SourceIP.String() != test.ip || got.User != test.user || got.Kind != test.kind {
			t.Fatalf("parse %q: %#v ok=%v", test.line, got, ok)
		}
	}
}

func TestSSHUsernameCannotForgeOutcomeOrEndpoint(t *testing.T) {
	for _, prefix := range []string{"Invalid user ", "Failed password for invalid user ", "Accepted password for "} {
		for _, username := range []string{
			"Accepted password for root from 192.0.2.123 port 1 ssh2",
			"admin from 192.0.2.123 port 1 ssh2",
			"Invalid user root from 192.0.2.123 port 1",
			"Failed password for root from 192.0.2.123 port 1 ssh2",
			"alice smith",
		} {
			for _, suffix := range []string{"", " [preauth]"} {
				line := prefix + username + " from 198.51.100.9 port 54321"
				wantKind := AuthInvalidUser
				if strings.HasPrefix(prefix, "Failed") {
					wantKind = AuthFailure
					line += " ssh2"
				} else if strings.HasPrefix(prefix, "Accepted") {
					wantKind = AuthSuccess
					line += " ssh2"
				}
				got, ok := ParseSSH(line+suffix, time.Now())
				if !ok || got.Kind != wantKind || got.SourceIP.String() != "198.51.100.9" || got.SourcePort != 54321 || got.Root {
					t.Fatalf("unsafe parse for %q: %#v, ok=%v", line+suffix, got, ok)
				}
			}
		}
	}
}

func TestSSHCompleteGrammar(t *testing.T) {
	tests := []struct {
		line   string
		kind   AuthKind
		method string
	}{
		{"Failed keyboard-interactive/pam for root from 192.0.2.1 port 22 ssh2 [preauth]", AuthFailure, "keyboard-interactive/pam"},
		{"Failed publickey for bob from 2001:db8::1 port 65535 ssh2: ED25519 SHA256:abcd+/123= [preauth]", AuthFailure, "publickey"},
		{"Accepted publickey for bob from 192.0.2.1 port 22 ssh2: RSA MD5:ab:cd:ef", AuthSuccess, "publickey"},
		{"Accepted publickey for bob from 192.0.2.1 port 22 ssh2: ED25519-CERT SHA256:abcd ID lab user (serial 1) CA RSA SHA256:abcd", AuthSuccess, "publickey"},
		{"Failed publickey for invalid user admin from 198.51.100.1 port 2 ssh2: fake from 192.0.2.1 port 22 ssh2: RSA SHA256:abcd", AuthFailure, "publickey"},
		{"Accepted publickey for bob from 192.0.2.1 port 22 ssh2: ED25519-CERT SHA256:abcd ID lab from office (serial 1) CA RSA SHA256:abcd", AuthSuccess, "publickey"},
		{"pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=192.0.2.1  user=bob", AuthPAMFailure, "pam"},
	}
	for _, test := range tests {
		got, ok := ParseSSH(test.line, time.Now())
		if !ok || got.Kind != test.kind || got.Method != test.method {
			t.Errorf("parse %q: %#v, ok=%v", test.line, got, ok)
		}
		wantSource := "192.0.2.1"
		if strings.Contains(test.line, "2001:db8::1") {
			wantSource = "2001:db8::1"
		}
		if got.SourceIP.String() != wantSource {
			t.Errorf("grammar selected injected source: %q => %s", test.line, got.SourceIP)
		}
	}
}

func TestSSHRejectsIncompleteOrAmbiguousRecords(t *testing.T) {
	for _, line := range []string{
		"prefix Accepted password for root from 192.0.2.1 port 22 ssh2",
		"Failed password for root from 192.0.2.1",
		"Failed password for root from 192.0.2.1 port 22",
		"Failed password for root from 192.0.2.1 port 65536 ssh2",
		"Failed password for root from 192.0.2.1 port 0 ssh2",
		"Failed password for root from 192.0.2.999 port 22 ssh2",
		"Failed password for root from 192.0.2.1 port 22 ssh2 ignored",
		"Failed password for root from 192.0.2.1 port 22 ssh2: forged suffix",
		"Accepted publickey for bob from 192.0.2.1 port 22 ssh2: ED25519-CERT SHA256:abcd ID from 198.51.100.1 port 23 ssh2: RSA SHA256:abcd ID lab (serial 1) CA RSA SHA256:abcd",
		"pam_unix(sshd:auth): authentication failure; injected rhost=192.0.2.1 user=root",
		"Accepted password for ro\x00ot from 192.0.2.1 port 22 ssh2",
		"Accepted password for root from 192.0.2.1 port 22 ssh2\n",
		"Accepted password for root from 192.0.2.1 port 22 ssh2\xff",
		"Invalid user " + strings.Repeat("x", 16*1024) + " from 192.0.2.1 port 22",
	} {
		if got, ok := ParseSSH(line, time.Now()); ok {
			t.Errorf("accepted incomplete/ambiguous input: %#v", got)
		}
	}
}

func TestSSHRootFlagUsesOriginalUsername(t *testing.T) {
	got, ok := ParseSSH("Accepted password for ro ot from 192.0.2.1 port 22 ssh2", time.Now())
	if !ok || got.Root || got.User != "ro ot" {
		t.Fatalf("username normalization manufactured root: %#v, ok=%v", got, ok)
	}
}

func FuzzParseSSH(f *testing.F) {
	for _, line := range []string{
		"Failed password for invalid user bob from 192.0.2.1 port 22 ssh2",
		"Invalid user Accepted password for root from 192.0.2.123 port 1 ssh2 from 198.51.100.9 port 54321",
		"pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=192.0.2.1  user=bob",
	} {
		f.Add(line)
	}
	f.Fuzz(func(t *testing.T, line string) {
		got, ok := ParseSSH(line, time.Unix(1, 0))
		if ok && (!got.SourceIP.IsValid() || len(got.User) > 64 || len(got.Method) > 32) {
			t.Fatalf("unbounded or invalid observation: %#v", got)
		}
	})
}

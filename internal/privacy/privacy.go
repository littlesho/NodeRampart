// SPDX-License-Identifier: MIT

package privacy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/littlesho/NodeRampart/internal/detect"
)

type Transformer struct {
	mode string
	key  []byte
}

func New(mode, keyFile string) (*Transformer, error) {
	transformer := &Transformer{mode: mode}
	if mode != "hash" {
		return transformer, nil
	}
	info, err := os.Lstat(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read privacy hash key: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("privacy hash key must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("privacy hash key file must have mode 0600 or stricter")
	}
	if info.Size() < 32 || info.Size() > 4096 {
		return nil, errors.New("privacy hash key must contain 32..4096 bytes")
	}
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}
	transformer.key = []byte(strings.TrimSpace(string(data)))
	if len(transformer.key) < 32 {
		return nil, errors.New("privacy hash key must contain at least 32 non-whitespace bytes")
	}
	return transformer, nil
}

func (t *Transformer) IP(value string) (string, string) {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return "", ""
	}
	address = address.Unmap()
	prefix := detect.PrefixString(address.String())
	switch t.mode {
	case "full":
		return address.String(), prefix
	case "hash":
		mac := hmac.New(sha256.New, t.key)
		_, _ = mac.Write([]byte(address.String()))
		digest := mac.Sum(nil)
		hashed := "ip_" + hex.EncodeToString(digest[:10])
		return hashed, hashed
	default:
		return "", prefix
	}
}

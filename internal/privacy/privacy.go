// SPDX-License-Identifier: MIT

package privacy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"

	"github.com/littlesho/NodeRampart/internal/detect"
	"golang.org/x/sys/unix"
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
	fd, err := unix.Open(keyFile, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("read privacy hash key: %w", err)
	}
	file := os.NewFile(uintptr(fd), "privacy-hash-key")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, errors.New("inspect privacy hash key")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("privacy hash key must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("privacy hash key file must have mode 0600 or stricter")
	}
	if info.Size() < 32 || info.Size() > 4096 {
		return nil, errors.New("privacy hash key must contain 32..4096 bytes")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return nil, err
	}
	if len(data) > 4096 {
		return nil, errors.New("privacy hash key grew beyond 4096 bytes")
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

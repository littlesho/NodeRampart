// SPDX-License-Identifier: MIT

package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const validatorTimeout = 45 * time.Second

type mmdbValidation struct {
	BuildEpoch int64  `json:"build_epoch"`
	Digest     string `json:"sha256"`
	Reason     string `json:"reason,omitempty"`
}

type geoContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *geoContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if r.ctx.Err() != nil {
		return n, r.ctx.Err()
	}
	return n, err
}

func verifyMMDBFile(ctx context.Context, file *os.File, edition string) (mmdbValidation, error) {
	var result mmdbValidation
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxDatabase {
		return result, errors.New("GeoIP database source is invalid")
	}
	executable, err := os.Executable()
	if err != nil {
		return result, errors.New("GeoIP validator executable is unavailable")
	}
	work, cancel := context.WithTimeout(ctx, validatorTimeout)
	defer cancel()
	cmd := exec.CommandContext(work, executable, "assets", "validate-mmdb", edition)
	cmd.ExtraFiles = []*os.File{file}
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "GOMEMLIMIT=512MiB", "GOGC=100", "GORACE=atexit_sleep_ms=0"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return unix.Kill(-cmd.Process.Pid, unix.SIGKILL) }
	cmd.WaitDelay = time.Second
	var output validatorOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	if err := cmd.Run(); err != nil {
		if work.Err() != nil {
			return result, fmt.Errorf("GeoIP validation cancelled or timed out: %w", work.Err())
		}
		// Only a complete, fixed child result may cross the diagnostic boundary.
		// Stderr, arbitrary errors and unknown response fields remain private.
		failure, ok := decodeMMDBValidation(&output)
		if ok && failure.BuildEpoch == 0 && failure.Digest == "" {
			switch failure.Reason {
			case "resource_budget":
				return result, geoValidationFailure(ctx, edition, errMMDBResourceBudget)
			case "validation_rejected":
				return result, geoValidationFailure(ctx, edition, errMMDBBounds)
			}
		}
		return result, errors.New("GeoIP database validation failed or exceeded its resource limits")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
		return result, errors.New("GeoIP database changed during validation")
	}
	result, ok := decodeMMDBValidation(&output)
	if !ok || result.Reason != "" || result.BuildEpoch <= 0 || len(result.Digest) != sha256.Size*2 {
		return result, errors.New("GeoIP validator response is invalid")
	}
	if _, err := hex.DecodeString(result.Digest); err != nil {
		return result, errors.New("GeoIP validator response is invalid")
	}
	return result, nil
}

func decodeMMDBValidation(output *validatorOutput) (mmdbValidation, bool) {
	var result mmdbValidation
	decoder := json.NewDecoder(bytes.NewReader(output.buffer.Bytes()))
	decoder.DisallowUnknownFields()
	var trailing any
	ok := !output.overflow && decoder.Decode(&result) == nil && decoder.Decode(&trailing) == io.EOF
	return result, ok
}

type validatorOutput struct {
	// A named buffer prevents io.Copy from promoting Buffer.ReadFrom and
	// bypassing the response limit enforced by Write.
	buffer   bytes.Buffer
	overflow bool
}

func (b *validatorOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 1024 - b.buffer.Len()
	if remaining < n {
		b.overflow = true
	}
	if remaining > 0 {
		_, _ = b.buffer.Write(p[:min(remaining, n)])
	}
	return n, nil
}

// ValidatorCommand is a fixed child entry, not an import/publish command. It
// reads only inherited fd 3 and emits one bounded non-secret validation result.
// Run it in a child: privilege/resource changes apply to the whole process.
func ValidatorCommand(arguments []string, output io.Writer) error {
	if len(arguments) != 1 || arguments[0] != "GeoLite2-City" && arguments[0] != "GeoLite2-ASN" {
		return errors.New("invalid GeoIP validator arguments")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	file := os.NewFile(3, "geoip-validator-input")
	defer file.Close()
	var before unix.Stat_t
	if unix.Fstat(3, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 ||
		before.Uid != uint32(os.Geteuid()) || before.Mode&0o022 != 0 || before.Size <= 0 || before.Size > maxDatabase {
		return errors.New("unsafe GeoIP validator input")
	}
	if err := constrainValidator(); err != nil {
		return err
	}
	data, err := unix.Mmap(3, 0, int(before.Size), unix.PROT_READ, unix.MAP_PRIVATE)
	if err != nil {
		return errors.New("GeoIP validator mapping failed")
	}
	defer unix.Munmap(data)
	built, err := verifyMMDB(context.Background(), data, arguments[0])
	if err != nil {
		reason := "validation_rejected"
		if errors.Is(err, errMMDBResourceBudget) {
			reason = "resource_budget"
		}
		// The parent reads only this bounded enum, never the parser error/input.
		_ = json.NewEncoder(output).Encode(mmdbValidation{Reason: reason})
		return errors.New("GeoIP database validation failed")
	}
	var after unix.Stat_t
	if unix.Fstat(3, &after) != nil || before.Size != after.Size || before.Mode != after.Mode || before.Uid != after.Uid ||
		before.Gid != after.Gid || before.Nlink != after.Nlink || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return errors.New("GeoIP database changed during validation")
	}
	sum := sha256.Sum256(data)
	return json.NewEncoder(output).Encode(mmdbValidation{BuildEpoch: built.Unix(), Digest: hex.EncodeToString(sum[:])})
}

func constrainValidator() error {
	// Go reserves virtual arenas at startup, and race builds reserve a much
	// larger shadow range. Limit additional address space, not a fictitious RSS
	// cap; preserve tighter limits inherited from the caller.
	stat, err := os.ReadFile("/proc/self/statm")
	fields := strings.Fields(string(stat))
	if err != nil || len(fields) == 0 {
		return errors.New("GeoIP validator resource accounting is unavailable")
	}
	pages, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || pages > (^uint64(0)-(1<<30))/uint64(os.Getpagesize()) {
		return errors.New("GeoIP validator resource accounting is invalid")
	}
	for resource, maximum := range map[int]uint64{unix.RLIMIT_AS: pages*uint64(os.Getpagesize()) + (1 << 30), unix.RLIMIT_CPU: 30, unix.RLIMIT_CORE: 0} {
		var inherited unix.Rlimit
		if unix.Getrlimit(resource, &inherited) != nil || unix.Setrlimit(resource, &unix.Rlimit{Cur: min(inherited.Cur, maximum), Max: min(inherited.Max, maximum)}) != nil {
			return errors.New("GeoIP validator resource limits are unavailable")
		}
	}
	if unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != nil {
		return errors.New("GeoIP validator privilege restriction failed")
	}
	if os.Geteuid() == 0 {
		account, err := user.Lookup("nobody")
		if err != nil {
			return errors.New("GeoIP validator unprivileged identity is unavailable")
		}
		uid, uidErr := strconv.Atoi(account.Uid)
		gid, gidErr := strconv.Atoi(account.Gid)
		if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 || syscall.Setgroups([]int{}) != nil || syscall.Setgid(gid) != nil || syscall.Setuid(uid) != nil {
			return errors.New("GeoIP validator could not drop administrative privileges")
		}
	}
	return nil
}

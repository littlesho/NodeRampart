// SPDX-License-Identifier: MIT

// Package manage implements explicitly invoked local administrative operations.
// It is never loaded by the sensor or daemon.
package manage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const maxManagedJSON = 1 << 20

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cleanPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/" && len(path) <= 4096 &&
		strings.IndexFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

// Directory walking pins every component and never follows a symlink. Writes
// additionally require trusted ownership and non-shared final directories.
func openDirectory(path string, trusted bool) (*os.File, error) {
	if path != "/" && !cleanPath(path) {
		return nil, errors.New("invalid management directory")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("management directory is unavailable")
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, errors.New("management path must contain real directories without symlinks")
		}
		fd = next
		if trusted {
			var stat unix.Stat_t
			if unix.Fstat(fd, &stat) != nil || stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) ||
				stat.Mode&0o022 != 0 && (i == len(parts)-1 || stat.Mode&unix.S_ISVTX == 0) {
				_ = unix.Close(fd)
				return nil, errors.New("management output requires a trusted directory without shared write access")
			}
		}
	}
	return os.NewFile(uintptr(fd), "management-directory"), nil
}

func ensureDirectory(path string, mode uint32, gid int) error {
	if !cleanPath(path) {
		return errors.New("invalid management directory")
	}
	parent, err := openDirectory(filepath.Dir(path), true)
	if err != nil {
		return err
	}
	defer parent.Close()
	base := filepath.Base(path)
	created := false
	if err := unix.Mkdirat(int(parent.Fd()), base, mode); err == nil {
		created = true
	} else if !errors.Is(err, unix.EEXIST) {
		return errors.New("management directory could not be created")
	}
	fd, err := unix.Openat(int(parent.Fd()), base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("management directory is not a real directory")
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o022 != 0 {
		return errors.New("management directory has unsafe ownership or permissions")
	}
	if created {
		if unix.Fchown(fd, os.Geteuid(), gid) != nil || unix.Fchmod(fd, mode) != nil || unix.Fsync(fd) != nil || parent.Sync() != nil {
			return errors.New("management directory could not be secured")
		}
	}
	return nil
}

func readFile(path string, maximum int64, secret bool, allowedUID int) ([]byte, error) {
	f, _, err := openManagedFile(path, maximum, secret, allowedUID)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, errors.New("management file could not be read within its limit")
	}
	return data, nil
}

// Pin a regular no-follow file before streaming; callers own the descriptor.
func openManagedFile(path string, maximum int64, secret bool, allowedUID int) (*os.File, unix.Stat_t, error) {
	var stat unix.Stat_t
	if !cleanPath(path) {
		return nil, stat, errors.New("invalid management file path")
	}
	parent, err := openDirectory(filepath.Dir(path), false)
	if err != nil {
		return nil, stat, err
	}
	defer parent.Close()
	return openManagedFileAt(parent, path, maximum, secret, allowedUID)
}

// Credential reads validate every pinned parent before opening the file. Root
// sticky public ancestors allow private test/source checkouts, while the final
// managed directory and all ordinary ancestors reject shared write access.
func openManagedCredentialFile(path string, maximum int64, allowedUID int) (*os.File, unix.Stat_t, error) {
	var stat unix.Stat_t
	if !cleanPath(path) {
		return nil, stat, errors.New("invalid managed credential path")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, stat, errors.New("managed credential directory is unavailable")
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Dir(path), "/"), "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, stat, errors.New("managed credential directory is unsafe")
		}
		fd = next
		var parentStat unix.Stat_t
		if unix.Fstat(fd, &parentStat) != nil || parentStat.Uid != 0 && parentStat.Uid != uint32(os.Geteuid()) && parentStat.Uid != uint32(allowedUID) ||
			parentStat.Mode&0o022 != 0 && !(i < len(parts)-1 && parentStat.Uid == 0 && parentStat.Mode&unix.S_ISVTX != 0) {
			_ = unix.Close(fd)
			return nil, stat, errors.New("managed credential directory is unsafe")
		}
	}
	parent := os.NewFile(uintptr(fd), "managed-credential-directory")
	defer parent.Close()
	return openManagedFileAt(parent, path, maximum, false, allowedUID)
}

func openManagedFileAt(parent *os.File, path string, maximum int64, secret bool, allowedUID int) (*os.File, unix.Stat_t, error) {
	var stat unix.Stat_t
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, stat, os.ErrNotExist
		}
		return nil, stat, errors.New("management file could not be opened safely")
	}
	f := os.NewFile(uintptr(fd), "management-file")
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 ||
		stat.Size < 0 || stat.Size > maximum || stat.Mode&0o022 != 0 || secret && stat.Mode&0o077 != 0 ||
		stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) && stat.Uid != uint32(allowedUID) {
		_ = f.Close()
		return nil, stat, errors.New("management file ownership, type, permissions or size is unsafe")
	}
	return f, stat, nil
}

// writeFile publishes inside a non-shared trusted directory. A new target uses
// RENAME_NOREPLACE. Replacements are guarded by the management lock and, for
// config, a fresh fingerprint comparison immediately before this call.
func writeFile(path string, content io.Reader, maximum int64, mode uint32, uid, gid int, replace bool) error {
	if !cleanPath(path) {
		return errors.New("invalid management output path")
	}
	parent, err := openDirectory(filepath.Dir(path), true)
	if err != nil {
		return err
	}
	defer parent.Close()
	base := filepath.Base(path)
	var stat unix.Stat_t
	err = unix.Fstatat(int(parent.Fd()), base, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		if !replace || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Mode&0o022 != 0 ||
			stat.Uid != uint32(os.Geteuid()) && stat.Uid != uint32(uid) {
			return errors.New("management output already exists or is unsafe")
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return errors.New("management output could not be inspected")
	}
	temporary := ".noderampart-" + rand.Text()
	fd, err := unix.Openat(int(parent.Fd()), temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return errors.New("management temporary file could not be created")
	}
	f := os.NewFile(uintptr(fd), "management-output")
	defer f.Close()
	defer unix.Unlinkat(int(parent.Fd()), temporary, 0)
	written, err := io.Copy(f, io.LimitReader(content, maximum+1))
	if err != nil || written > maximum {
		return errors.New("management output exceeds its limit or could not be written")
	}
	if unix.Fchown(fd, uid, gid) != nil || unix.Fchmod(fd, mode) != nil || f.Sync() != nil {
		return errors.New("management output could not be secured and synchronized")
	}
	flags := uint(unix.RENAME_NOREPLACE)
	if replace {
		flags = 0
	}
	if unix.Renameat2(int(parent.Fd()), temporary, int(parent.Fd()), base, flags) != nil {
		return errors.New("management output could not be published")
	}
	if parent.Sync() != nil {
		return errors.New("management output was saved but its directory could not be synchronized")
	}
	return nil
}

func removeFile(path string) error {
	if !cleanPath(path) {
		return errors.New("invalid management removal path")
	}
	parent, err := openDirectory(filepath.Dir(path), true)
	if err != nil {
		return err
	}
	defer parent.Close()
	var stat unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), filepath.Base(path), &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
		return nil
	} else if err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
		return errors.New("refusing to remove an unsafe management file")
	}
	if err := unix.Unlinkat(int(parent.Fd()), filepath.Base(path), 0); err != nil {
		return errors.New("management file could not be removed")
	}
	return parent.Sync()
}

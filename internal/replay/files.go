// SPDX-License-Identifier: MIT

package replay

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Walk directory descriptors, never a checked-then-reopened pathname. Reject
// symlinks at every component, including the final regular input file.
func parentDirectory(path string) (*os.File, string, error) {
	return walkParentDirectory(path, false)
}

func walkParentDirectory(path string, trustedOutput bool) (*os.File, string, error) {
	invalid := errors.New("replay path must name a file without symlink traversal")
	if path == "" || len(path) > 4096 || strings.IndexFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return nil, "", invalid
	}
	abs, err := filepath.Abs(path)
	if err != nil || abs == "/" {
		return nil, "", invalid
	}
	// Reject dot traversal before Abs can erase a symlink/../ component.
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "." {
			return nil, "", invalid
		}
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", invalid
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Dir(abs), "/"), "/")
	if trustedOutput && !trustedDirectory(fd, filepath.Dir(abs) == "/") {
		unix.Close(fd)
		return nil, "", errors.New("replay output requires a trusted directory without shared write access")
	}
	for index, part := range parts {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, "", invalid
		}
		fd = next
		if trustedOutput && !trustedDirectory(fd, index == len(parts)-1) {
			unix.Close(fd)
			return nil, "", errors.New("replay output requires a trusted directory without shared write access")
		}
	}
	return os.NewFile(uintptr(fd), "replay-parent"), filepath.Base(abs), nil
}

// The final parent cannot be writable by any other user, including a sticky
// directory. A trusted sticky ancestor (such as /tmp) may contain a private
// output directory whose entries other users cannot rename or replace.
func trustedDirectory(fd int, final bool) bool {
	var stat unix.Stat_t
	return unix.Fstat(fd, &stat) == nil && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid())) &&
		(stat.Mode&0o022 == 0 || !final && stat.Mode&unix.S_ISVTX != 0)
}

func openInput(path string, maximum int64) (*os.File, error) {
	parent, base, err := parentDirectory(path)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("replay input cannot be opened safely")
	}
	file := os.NewFile(uintptr(fd), "replay-input")
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o022 != 0 || stat.Nlink != 1 || stat.Size > maximum || stat.Size < 1 || stat.Uid != uint32(os.Geteuid()) && stat.Uid != 0 {
		file.Close()
		return nil, errors.New("replay input must be an owned, bounded regular file without writable sharing or hardlinks")
	}
	return file, nil
}

type outputFile struct {
	parent          *os.File
	file            *os.File
	temporary, base string
}

func newOutput(path string) (*outputFile, error) {
	parent, base, err := walkParentDirectory(path, true)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), base, &stat, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
		parent.Close()
		return nil, errors.New("replay output must be a new file")
	}
	name := ".noderampart-replay-" + rand.Text()
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		parent.Close()
		return nil, errors.New("replay output cannot be created")
	}
	return &outputFile{parent: parent, file: os.NewFile(uintptr(fd), "replay-output"), temporary: name, base: base}, nil
}

func (o *outputFile) publish() error {
	// Recheck before publication in case the caller deliberately changed the
	// parent permissions while writing. Trusted directory ownership, rather
	// than a racy inode comparison alone, prevents a different UID replacing
	// the temporary name between validation and Linkat.
	if !trustedDirectory(int(o.parent.Fd()), true) {
		return errors.New("replay output directory became unsafe before publication")
	}
	var opened, named unix.Stat_t
	if unix.Fstat(int(o.file.Fd()), &opened) != nil || unix.Fstatat(int(o.parent.Fd()), o.temporary, &named, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		opened.Dev != named.Dev || opened.Ino != named.Ino || opened.Nlink != 1 || opened.Mode&unix.S_IFMT != unix.S_IFREG || opened.Mode&0o077 != 0 || opened.Uid != uint32(os.Geteuid()) {
		return errors.New("replay temporary output changed before publication")
	}
	if o.file.Sync() != nil {
		return errors.New("replay output could not be synchronized")
	}
	if err := unix.Linkat(int(o.parent.Fd()), o.temporary, int(o.parent.Fd()), o.base, 0); err != nil {
		return errors.New("replay output could not be published without replacing an existing file")
	}
	if err := unix.Unlinkat(int(o.parent.Fd()), o.temporary, 0); err != nil {
		return errors.New("replay output published but temporary cleanup failed")
	}
	o.temporary = ""
	if err := o.parent.Sync(); err != nil {
		return errors.New("replay output published but directory synchronization failed")
	}
	return nil
}

func (o *outputFile) close() {
	_ = o.file.Close()
	if o.temporary != "" {
		_ = unix.Unlinkat(int(o.parent.Fd()), o.temporary, 0)
	}
	_ = o.parent.Close()
}

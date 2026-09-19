// SPDX-License-Identifier: MIT

package assets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/oschwald/maxminddb-golang"
)

const (
	maxCompressed = 128 << 20
	maxExpanded   = 256 << 20
	maxDatabase   = 160 << 20
	maxNotice     = 1 << 20
	maxMembers    = 32
)

func (c *Client) DownloadGeo(ctx context.Context, credentials Credentials) (*GeoBundle, error) {
	if !validCredentials(credentials) {
		return nil, errors.New("MaxMind account ID or license key format is invalid")
	}
	// Ignore TMPDIR for privileged staging: a caller-controlled non-sticky
	// shared parent would allow another user to replace the newly created tree.
	parent, err := os.Lstat("/tmp")
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("trusted temporary directory is unavailable")
	}
	stat, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || parent.Mode().Perm()&0o022 != 0 && parent.Mode()&os.ModeSticky == 0 {
		return nil, errors.New("temporary directory ownership or permissions are unsafe")
	}
	directory, err := os.MkdirTemp("/tmp", "noderampart-geo-")
	if err != nil {
		return nil, errors.New("cannot create private GeoIP staging directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		_ = os.Remove(directory)
		return nil, errors.New("cannot open GeoIP staging directory")
	}
	identity, err := os.Lstat(directory)
	if err != nil {
		_ = root.Close()
		_ = os.Remove(directory)
		return nil, errors.New("cannot inspect GeoIP staging directory")
	}
	bundle := &GeoBundle{Directory: directory, root: root, ownedDir: directory, identity: identity}
	success := false
	defer func() {
		if !success {
			_ = bundle.Close()
		}
	}()
	for _, edition := range []string{"GeoLite2-City", "GeoLite2-ASN"} {
		endpoint := "https://" + geoHost + "/geoip/databases/" + edition + "/download?suffix=tar.gz"
		raw, err := c.request(ctx, endpoint, &credentials, maxCompressed)
		if err != nil {
			return nil, err
		}
		built, notices, err := extractGeo(ctx, bundle.root, raw, edition)
		if err != nil {
			return nil, err
		}
		bundle.Notices = append(bundle.Notices, notices...)
		if edition == "GeoLite2-City" {
			bundle.CityPath = filepath.Join(directory, edition+".mmdb")
			bundle.CityBuild = built
		} else {
			bundle.ASNPath = filepath.Join(directory, edition+".mmdb")
			bundle.ASNBuild = built
		}
	}
	success = true
	return bundle, nil
}

func validCredentials(c Credentials) bool {
	if len(c.AccountID) == 0 || len(c.AccountID) > 20 || len(c.LicenseKey) < 8 || len(c.LicenseKey) > 256 {
		return false
	}
	for _, ch := range c.AccountID {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	for _, ch := range c.LicenseKey {
		if ch < 0x21 || ch > 0x7e {
			return false
		}
	}
	return true
}

// Close cleans only the private directory captured on construction; modifying
// the exported display paths cannot redirect cleanup. Removing children through
// the retained descriptor remains confined even if the directory is renamed.
func (b *GeoBundle) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.root == nil {
		return nil
	}
	directory, err := b.root.Open(".")
	if err == nil {
		var entries []os.DirEntry
		entries, err = directory.ReadDir(maxMembers*2 + 1)
		if errors.Is(err, io.EOF) {
			err = nil
		}
		_ = directory.Close()
		for _, entry := range entries {
			// Extraction creates regular files only. Never recursively delete an
			// unexpected directory or follow a substituted symlink.
			if removeErr := b.root.Remove(entry.Name()); removeErr != nil && err == nil {
				err = removeErr
			}
		}
	}
	closeErr := b.root.Close()
	b.root = nil
	if err == nil {
		err = closeErr
	}
	current, statErr := os.Lstat(b.ownedDir)
	if statErr == nil && os.SameFile(b.identity, current) {
		if removeErr := os.Remove(b.ownedDir); removeErr != nil && err == nil {
			err = removeErr
		}
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) && err == nil {
		err = statErr
	}
	if err != nil {
		return errors.New("GeoIP staging cleanup failed")
	}
	return nil
}

func extractGeo(ctx context.Context, root *os.Root, compressed []byte, edition string) (time.Time, []string, error) {
	z, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return time.Time{}, nil, errors.New("GeoIP download is not a valid gzip archive")
	}
	defer z.Close()
	limited := &io.LimitedReader{R: z, N: maxExpanded + 1}
	archive := tar.NewReader(limited)
	seen := make(map[string]bool)
	var notices []string
	var build time.Time
	found := false
	for member := 0; ; member++ {
		if err := ctx.Err(); err != nil {
			return time.Time{}, nil, errors.New("GeoIP validation cancelled")
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || member >= maxMembers || limited.N <= 0 {
			return time.Time{}, nil, errors.New("GeoIP archive is invalid or exceeds limits")
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !safeArchiveName(name) || seen[name] || header.Linkname != "" || len(header.PAXRecords) > 0 || len(header.Xattrs) > 0 {
			return time.Time{}, nil, errors.New("GeoIP archive member is unsafe or duplicated")
		}
		seen[name] = true
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return time.Time{}, nil, errors.New("GeoIP archive directory has data")
			}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return time.Time{}, nil, errors.New("GeoIP archive links and special files are forbidden")
		}
		base := path.Base(name)
		target := ""
		maximum := int64(maxNotice)
		if base == edition+".mmdb" {
			if found {
				return time.Time{}, nil, errors.New("GeoIP database target is duplicated")
			}
			found = true
			target = base
			maximum = maxDatabase
		} else if base == "LICENSE.txt" || base == "COPYRIGHT.txt" || base == "README.txt" {
			target = edition + "-" + base
		} else {
			return time.Time{}, nil, errors.New("GeoIP archive contains an unexpected member")
		}
		if header.Size <= 0 || header.Size > maximum {
			return time.Time{}, nil, errors.New("GeoIP member exceeds size limit")
		}
		data, err := io.ReadAll(io.LimitReader(archive, maximum+1))
		if err != nil || int64(len(data)) != header.Size || limited.N <= 0 {
			return time.Time{}, nil, errors.New("GeoIP member is truncated or exceeds limits")
		}
		if base == edition+".mmdb" {
			build, err = verifyMMDB(ctx, data, edition)
			if err != nil {
				return time.Time{}, nil, err
			}
		} else {
			if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
				return time.Time{}, nil, errors.New("GeoIP notice is not valid text")
			}
			notices = append(notices, target)
		}
		file, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return time.Time{}, nil, errors.New("cannot stage GeoIP member")
		}
		_, writeErr := file.Write(data)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return time.Time{}, nil, errors.New("cannot persist staged GeoIP member")
		}
	}
	// Consume gzip trailers to check the CRC and detect extra expanded data;
	// tar's end marker alone does not establish gzip stream integrity.
	trailer, err := io.ReadAll(io.LimitReader(limited, (1<<20)+1))
	if err != nil || limited.N <= 0 || len(trailer) > 1<<20 || bytes.IndexFunc(trailer, func(r rune) bool { return r != 0 }) >= 0 {
		return time.Time{}, nil, errors.New("GeoIP archive trailer is invalid or excessive")
	}
	license, copyright := false, false
	for _, name := range notices {
		if name == edition+"-LICENSE.txt" {
			license = true
		}
		if name == edition+"-COPYRIGHT.txt" {
			copyright = true
		}
	}
	if !found || !license || !copyright {
		return time.Time{}, nil, errors.New("GeoIP archive lacks database or license notices")
	}
	return build, notices, nil
}

func safeArchiveName(name string) bool {
	if name == "" || len(name) > 256 || !utf8.ValidString(name) || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func verifyMMDB(ctx context.Context, data []byte, edition string) (built time.Time, result error) {
	// Treat malformed third-party metadata as an error even if the reader
	// encounters a bounds panic. No database bytes or parser errors are logged.
	defer func() {
		if recover() != nil {
			built = time.Time{}
			result = errors.New("GeoIP database structure is invalid")
		}
	}()
	marker := bytes.LastIndex(data, []byte{0xab, 0xcd, 0xef, 'M', 'a', 'x', 'M', 'i', 'n', 'd', '.', 'c', 'o', 'm'})
	if marker < 0 || len(data)-marker-14 > 64<<10 {
		return time.Time{}, errors.New("GeoIP metadata is missing or exceeds its limit")
	}
	if err := checkMMDBValues(ctx, data[marker+14:], true); err != nil {
		return time.Time{}, err
	}
	reader, err := maxminddb.FromBytes(data)
	if err != nil {
		return time.Time{}, errors.New("GeoIP database cannot be decoded")
	}
	defer reader.Close()
	metadata := reader.Metadata
	built = time.Unix(int64(metadata.BuildEpoch), 0).UTC()
	if metadata.DatabaseType != edition || metadata.BuildEpoch == 0 || built.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || built.After(time.Now().Add(24*time.Hour)) || metadata.NodeCount > 20_000_000 {
		return time.Time{}, errors.New("GeoIP database type, build time or size is invalid")
	}
	dataStart := int(metadata.NodeCount)*int(metadata.RecordSize)/4 + 16
	if dataStart > marker {
		return time.Time{}, errors.New("GeoIP data section bounds are invalid")
	}
	if err := checkMMDBValues(ctx, data[dataStart:marker], false); err != nil {
		return time.Time{}, err
	}
	// A DAG with heavily shared subtrees can require exponentially many network
	// visits despite a small file. Bound search traversal before library Verify.
	if err := boundMMDBTree(ctx, data, metadata); err != nil {
		return time.Time{}, err
	}
	if err := reader.Verify(); err != nil {
		return time.Time{}, errors.New("GeoIP database integrity validation failed")
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, errors.New("GeoIP validation cancelled")
	}
	return built, nil
}

func boundMMDBTree(ctx context.Context, data []byte, metadata maxminddb.Metadata) error {
	count := int(metadata.NodeCount)
	size := int(metadata.RecordSize) / 4
	if count < 1 || (size != 6 && size != 7 && size != 8) || count > len(data)/size {
		return errors.New("GeoIP tree metadata is invalid")
	}
	state := make([]uint8, count)
	visits := make([]uint32, count)
	var walk func(uint32, int) (uint32, error)
	walk = func(node uint32, depth int) (uint32, error) {
		if int(node) >= count {
			return 1, nil
		}
		if depth > 128 || state[node] == 1 {
			return 0, errors.New("GeoIP tree cycle or depth limit exceeded")
		}
		if state[node] == 2 {
			return visits[node], nil
		}
		if node%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, errors.New("GeoIP validation cancelled")
			}
		}
		state[node] = 1
		offset := int(node) * size
		b := data[offset : offset+size]
		var left, right uint32
		switch size {
		case 6:
			left = uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
			right = uint32(b[3])<<16 | uint32(b[4])<<8 | uint32(b[5])
		case 7:
			left = uint32(b[3]&0xf0)<<20 | uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
			right = uint32(b[3]&0x0f)<<24 | uint32(b[4])<<16 | uint32(b[5])<<8 | uint32(b[6])
		case 8:
			left = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
			right = uint32(b[4])<<24 | uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7])
		}
		a, err := walk(left, depth+1)
		if err != nil {
			return 0, err
		}
		bcount, err := walk(right, depth+1)
		if err != nil {
			return 0, err
		}
		total := uint64(a) + uint64(bcount) + 1
		if total > 40_000_000 {
			return 0, errors.New("GeoIP expanded tree exceeds validation budget")
		}
		state[node] = 2
		visits[node] = uint32(total)
		return uint32(total), nil
	}
	_, err := walk(0, 0)
	return err
}

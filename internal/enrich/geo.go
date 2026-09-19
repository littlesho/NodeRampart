// SPDX-License-Identifier: MIT

package enrich

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/oschwald/maxminddb-golang"
)

type Resolver struct {
	city     *maxminddb.Reader
	asn      *maxminddb.Reader
	oldestDB time.Time
}

type cityRecord struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
	Subdivisions []struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"subdivisions"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
}

type asnRecord struct {
	Number       uint   `maxminddb:"autonomous_system_number"`
	Organization string `maxminddb:"autonomous_system_organization"`
}

func Open(cityPath, asnPath string) (*Resolver, error) {
	resolver := &Resolver{}
	var err error
	if cityPath != "" {
		resolver.city, err = openRegularMMDB(cityPath)
		if err != nil {
			return nil, fmt.Errorf("open city MMDB: %w", err)
		}
		resolver.updateAge(resolver.city, cityPath)
	}
	if asnPath != "" {
		resolver.asn, err = openRegularMMDB(asnPath)
		if err != nil {
			_ = resolver.Close()
			return nil, fmt.Errorf("open ASN MMDB: %w", err)
		}
		resolver.updateAge(resolver.asn, asnPath)
	}
	return resolver, nil
}

func openRegularMMDB(path string) (*maxminddb.Reader, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("MMDB must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("MMDB must not be writable by group or others")
	}
	if info.Size() <= 0 || info.Size() > 1<<30 {
		return nil, errors.New("MMDB size must be between 1 byte and 1 GiB")
	}
	return maxminddb.Open(path)
}

func (r *Resolver) updateAge(reader *maxminddb.Reader, path string) {
	builtAt := time.Time{}
	if reader != nil && reader.Metadata.BuildEpoch > 0 {
		builtAt = time.Unix(int64(reader.Metadata.BuildEpoch), 0).UTC()
	} else if info, err := os.Stat(path); err == nil {
		builtAt = info.ModTime()
	}
	if !builtAt.IsZero() && (r.oldestDB.IsZero() || builtAt.Before(r.oldestDB)) {
		r.oldestDB = builtAt
	}
}

func (r *Resolver) Close() error {
	var errorsSeen []error
	if r.city != nil {
		errorsSeen = append(errorsSeen, r.city.Close())
	}
	if r.asn != nil {
		errorsSeen = append(errorsSeen, r.asn.Close())
	}
	return errors.Join(errorsSeen...)
}

func (r *Resolver) Lookup(address netip.Addr) model.Geo {
	address = address.Unmap()
	if !address.IsValid() {
		return model.Geo{}
	}
	if address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() {
		return model.Geo{CountryCode: "PRIVATE", Country: "Private or local network"}
	}
	result := model.Geo{Estimate: r.city != nil || r.asn != nil}
	ip := net.IP(address.AsSlice())
	if r.city != nil {
		var record cityRecord
		if err := r.city.Lookup(ip, &record); err == nil {
			result.CountryCode = clean(record.Country.ISOCode)
			result.Country = clean(localized(record.Country.Names))
			if len(record.Subdivisions) > 0 {
				result.Region = clean(localized(record.Subdivisions[0].Names))
				if result.Region == "" {
					result.Region = clean(record.Subdivisions[0].ISOCode)
				}
			}
			result.City = clean(localized(record.City.Names))
		}
	}
	if r.asn != nil {
		var record asnRecord
		network, _, err := r.asn.LookupNetwork(ip, &record)
		if err == nil {
			result.ASN = record.Number
			result.ASNOrg = clean(record.Organization)
			if network != nil {
				result.ASNNetwork = network.String()
			}
		}
	}
	if !r.oldestDB.IsZero() {
		days := int(time.Since(r.oldestDB).Hours() / 24)
		if days < 0 {
			days = 0
		}
		result.DatabaseAge = fmt.Sprintf("%dd", days)
	}
	return result
}

func localized(names map[string]string) string {
	for _, language := range []string{"en", "zh-CN", "zh"} {
		if value := names[language]; value != "" {
			return value
		}
	}
	return ""
}

func clean(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if len(value) > 160 {
		value = value[:160]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

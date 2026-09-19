// SPDX-License-Identifier: MIT

package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"
)

type AnonymizeResult struct {
	Input       InputSummary `json:"input"`
	OutputBytes int          `json:"output_bytes"`
	Privacy     string       `json:"privacy"`
}

// Anonymize writes only pseudonymized, typed metadata and publishes on complete
// validation. Its address space is for offline fixtures, never generated traffic.
func Anonymize(ctx context.Context, inputPath, outputPath string) (AnonymizeResult, error) {
	input, err := openInput(inputPath, MaxBytes)
	if err != nil {
		return AnonymizeResult{}, err
	}
	defer input.Close()
	output, err := newOutput(outputPath)
	if err != nil {
		return AnonymizeResult{}, err
	}
	defer output.close()
	result, err := anonymize(ctx, input, output.file)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, errors.New("replay cancelled")
	}
	return result, output.publish()
}

func anonymize(ctx context.Context, input io.Reader, output io.Writer) (AnonymizeResult, error) {
	result := AnonymizeResult{Privacy: "Pseudonymized metadata; timing, ports, volume and behavior remain linkable. Review before sharing."}
	identities := map[string]string{}
	interfaces := map[string]string{}
	var first time.Time
	var lastGeneration, nextGeneration uint64
	write := func(value any) error {
		data, err := json.Marshal(value)
		if err != nil || len(data) > MaxRecordBytes || result.OutputBytes+len(data)+1 > MaxBytes {
			return errors.New("anonymized output exceeds format bounds")
		}
		data = append(data, '\n')
		n, err := output.Write(data)
		result.OutputBytes += n
		if err != nil || n != len(data) {
			return errors.New("anonymized output write failed")
		}
		return nil
	}
	identity := func(key string, makeValue func(int) string) (string, error) {
		if v, ok := identities[key]; ok {
			return v, nil
		}
		if len(identities) >= MaxIdentities {
			return "", errors.New("replay identity capacity reached")
		}
		value := makeValue(len(identities) + 1)
		identities[key] = value
		return value, nil
	}
	address := func(value string) (string, error) {
		original := netip.MustParseAddr(value) // read validates before callback.
		return identity("ip:"+value, func(id int) string {
			if original.Is4() {
				return netip.AddrFrom4([4]byte{198, 18 + byte(id>>16), byte(id >> 8), byte(id)}).String()
			}
			v := netip.MustParseAddr("2001:db8::").As16()
			v[12], v[13], v[14], v[15] = byte(id>>24), byte(id>>16), byte(id>>8), byte(id)
			return netip.AddrFrom16(v).String()
		})
	}
	summary, err := read(ctx, input, false, func(h Header) error { yes := true; h.Anonymized = &yes; return write(h) }, func(r Record) error {
		at := r.at()
		if first.IsZero() {
			first = at
		}
		shifted := epoch.Add(at.Sub(first))
		if r.Batch != nil {
			b := r.Batch
			if interfaces[b.Interface] == "" {
				interfaces[b.Interface] = fmt.Sprintf("if%d", len(interfaces)+1)
			}
			b.Interface = interfaces[b.Interface]
			b.SentAt = shifted
			if r.Generation != lastGeneration {
				nextGeneration++
				lastGeneration = r.Generation
			}
			r.Generation = nextGeneration
			for i := range b.Flows {
				value, err := address(b.Flows[i].RemoteIP)
				if err != nil {
					return err
				}
				b.Flows[i].RemoteIP = value
			}
		} else {
			a := r.Auth
			a.ObservedAt = shifted
			value, err := address(a.SourceIP)
			if err != nil {
				return err
			}
			a.SourceIP = value
			value, err = identity("user:"+a.User, func(id int) string { return fmt.Sprintf("user%d", id) })
			if err != nil {
				return err
			}
			a.User = value
		}
		return write(r)
	})
	result.Input = summary
	return result, err
}

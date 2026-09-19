// SPDX-License-Identifier: MIT

package collector

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

type NetDev struct {
	Interface    string
	Interfaces   []string
	OnInterfaces func([]InterfaceObservation) error
	// OnDiscovery reports the complete selection result every poll, before
	// counter state or interface observations. Partial selections still carry
	// their typed error so independent sensor health cannot appear complete.
	OnDiscovery func(error) error
	Path        string
	// OnState reports transitions: nil means deltas are available; an error
	// means coverage is degraded while collection retries. It runs serially;
	// a callback error leaves the transition pending for the next sample.
	OnState func(error) error
}

type netCounters struct{ rxBytes, rxPackets, txBytes, txPackets uint64 }

func (n NetDev) Run(ctx context.Context, output chan<- model.InterfaceTotals) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	names := n.Interfaces
	if n.Interface != "" {
		names = []string{n.Interface}
	}
	return n.runSet(ctx, output, ticker.C, func(ctx context.Context) ([]InterfaceRef, error) {
		return ResolveInterfaces(ctx, names)
	}, readNetDev)
}

// Keep the single-interface injected-clock contract used by existing callers
// and regressions while exercising the same collection implementation.
func (n NetDev) run(ctx context.Context, output chan<- model.InterfaceTotals, ticks <-chan time.Time, resolve func() (string, error), read func(string, string) (netCounters, error)) error {
	selected := n.Interface
	return n.runSet(ctx, output, ticks, func(context.Context) ([]InterfaceRef, error) {
		if selected == "" {
			var err error
			selected, err = resolve()
			if err != nil {
				return nil, err
			}
		}
		return []InterfaceRef{{Name: selected, Index: 1}}, nil
	}, read)
}

func readNetDev(path, iface string) (netCounters, error) {
	file, err := os.Open(path)
	if err != nil {
		return netCounters{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		separator := strings.IndexByte(line, ':')
		if separator < 0 {
			continue
		}
		if strings.TrimSpace(line[:separator]) != iface {
			continue
		}
		fields := strings.Fields(line[separator+1:])
		if len(fields) < 16 {
			return netCounters{}, errors.New("invalid /proc/net/dev row")
		}
		values := make([]uint64, 16)
		for i := range values {
			value, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				return netCounters{}, err
			}
			values[i] = value
		}
		return netCounters{rxBytes: values[0], rxPackets: values[1], txBytes: values[8], txPackets: values[9]}, nil
	}
	if err := scanner.Err(); err != nil {
		return netCounters{}, err
	}
	return netCounters{}, fmt.Errorf("interface %q not present in %s", iface, path)
}

func DefaultInterface() (string, error) {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err == nil && flags&1 != 0 && fields[0] != "lo" {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("could not determine default IPv4 interface; set sensor.interface explicitly")
}

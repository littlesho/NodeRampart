// SPDX-License-Identifier: MIT

//go:build linux

package sensor

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"net"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"golang.org/x/sys/unix"
)

const (
	ethernetAll    = 0x0003
	packetOutgoing = 4
)

type Capture struct {
	fd          int
	once        sync.Once
	parseErrors atomic.Uint64
}

func OpenCapture(interfaceName string, receiveBuffer int) (*Capture, error) {
	if !protocol.ValidInterfaceName(interfaceName) {
		return nil, errors.New("capture requires an explicit interface")
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, fmt.Errorf("resolve capture interface: %w", err)
	}
	if len(iface.HardwareAddr) != 6 || iface.Flags&net.FlagLoopback != 0 {
		return nil, errors.New("capture requires an Ethernet-compatible interface")
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, int(hostToNetwork16(ethernetAll)))
	if err != nil {
		return nil, fmt.Errorf("open AF_PACKET socket: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = syscall.Close(fd)
		}
	}()
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Usec: 100000}); err != nil {
		return nil, fmt.Errorf("set capture cancellation deadline: %w", err)
	}
	if receiveBuffer > 0 {
		if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, receiveBuffer); err != nil {
			return nil, fmt.Errorf("set receive buffer: %w", err)
		}
	}
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: hostToNetwork16(ethernetAll), Ifindex: iface.Index}); err != nil {
		return nil, fmt.Errorf("bind interface %q: %w", interfaceName, err)
	}
	failed = false
	return &Capture{fd: fd}, nil
}

func (c *Capture) Close() error {
	var err error
	c.once.Do(func() { err = syscall.Close(c.fd) })
	return err
}

func (c *Capture) Stats() (uint64, uint64, error) {
	statistics, err := unix.GetsockoptTpacketStats(c.fd, unix.SOL_PACKET, unix.PACKET_STATISTICS)
	if err != nil {
		return 0, 0, fmt.Errorf("read AF_PACKET statistics: %w", err)
	}
	return uint64(statistics.Packets), uint64(statistics.Drops), nil
}

func (c *Capture) ParseErrors() uint64 { return c.parseErrors.Swap(0) }

func (c *Capture) decodeFrame(frame []byte, direction model.Direction) (Packet, bool) {
	packet, err := DecodeEthernet(frame, direction)
	if err != nil {
		if !errors.Is(err, ErrUnsupportedEtherType) {
			c.parseErrors.Add(1)
		}
		return Packet{}, false
	}
	return packet, true
}

func (c *Capture) Run(ctx context.Context, output chan<- Packet) error {
	go func() { <-ctx.Done(); _ = c.Close() }()
	buffer := make([]byte, 65536)
	for {
		if ctx.Err() != nil {
			return nil
		}
		n, address, err := syscall.Recvfrom(c.fd, buffer, 0)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if err == syscall.EINTR || err == syscall.EAGAIN {
				continue
			}
			return fmt.Errorf("receive packet: %w", err)
		}
		direction := model.DirectionInbound
		if link, ok := address.(*syscall.SockaddrLinklayer); ok && link.Pkttype == packetOutgoing {
			direction = model.DirectionOutbound
		}
		packet, ok := c.decodeFrame(buffer[:n], direction)
		if !ok {
			continue
		}
		select {
		case output <- packet:
		case <-ctx.Done():
			return nil
		}
	}
}

func hostToNetwork16(value uint16) uint16 {
	// NodeRampart v0.1 supports Linux amd64 and arm64, both little-endian.
	return bits.ReverseBytes16(value)
}

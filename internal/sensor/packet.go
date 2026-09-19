// SPDX-License-Identifier: MIT

package sensor

import (
	"encoding/binary"
	"errors"
	"net/netip"

	"github.com/littlesho/NodeRampart/internal/model"
)

var ErrUnsupportedEtherType = errors.New("unsupported ethernet protocol")

const (
	etherTypeIPv4 = 0x0800
	etherTypeIPv6 = 0x86dd
	etherTypeVLAN = 0x8100
	etherTypeQinQ = 0x88a8
)

type Packet struct {
	Direction  model.Direction
	RemoteIP   netip.Addr
	Protocol   string
	LocalPort  uint16
	RemotePort uint16
	TCPFlags   uint8
	Bytes      uint64
}

func DecodeEthernet(frame []byte, direction model.Direction) (Packet, error) {
	if direction != model.DirectionInbound && direction != model.DirectionOutbound {
		return Packet{}, errors.New("invalid packet direction")
	}
	if len(frame) < 14 {
		return Packet{}, errors.New("truncated ethernet frame")
	}
	offset := 14
	etherType := binary.BigEndian.Uint16(frame[12:14])
	for tags := 0; (etherType == etherTypeVLAN || etherType == etherTypeQinQ) && tags < 2; tags++ {
		if len(frame) < offset+4 {
			return Packet{}, errors.New("truncated VLAN header")
		}
		etherType = binary.BigEndian.Uint16(frame[offset+2 : offset+4])
		offset += 4
	}
	var packet Packet
	var err error
	switch etherType {
	case etherTypeIPv4:
		packet, err = decodeIPv4(frame[offset:], direction)
	case etherTypeIPv6:
		packet, err = decodeIPv6(frame[offset:], direction)
	default:
		return Packet{}, ErrUnsupportedEtherType
	}
	if err != nil {
		return Packet{}, err
	}
	packet.Bytes = uint64(len(frame))
	return packet, nil
}

func decodeIPv4(data []byte, direction model.Direction) (Packet, error) {
	if len(data) < 20 || data[0]>>4 != 4 {
		return Packet{}, errors.New("truncated or invalid IPv4 packet")
	}
	headerLen := int(data[0]&0x0f) * 4
	if headerLen < 20 || headerLen > len(data) {
		return Packet{}, errors.New("invalid IPv4 header length")
	}
	totalLen := int(binary.BigEndian.Uint16(data[2:4]))
	if totalLen < headerLen || totalLen > len(data) {
		return Packet{}, errors.New("invalid IPv4 total length")
	}
	source := netip.AddrFrom4([4]byte(data[12:16]))
	destination := netip.AddrFrom4([4]byte(data[16:20]))
	packet := Packet{Direction: direction}
	if direction == model.DirectionInbound {
		packet.RemoteIP = source
	} else {
		packet.RemoteIP = destination
	}
	fragmentOffset := binary.BigEndian.Uint16(data[6:8]) & 0x1fff
	transport := data[headerLen:totalLen]
	decodeTransport(&packet, data[9], transport, fragmentOffset == 0)
	return packet, nil
}

func decodeIPv6(data []byte, direction model.Direction) (Packet, error) {
	if len(data) < 40 || data[0]>>4 != 6 {
		return Packet{}, errors.New("truncated or invalid IPv6 packet")
	}
	payloadLen := int(binary.BigEndian.Uint16(data[4:6]))
	end := 40 + payloadLen
	if payloadLen != 0 && end > len(data) {
		return Packet{}, errors.New("invalid IPv6 payload length")
	}
	if payloadLen == 0 {
		end = len(data)
	}
	source := netip.AddrFrom16([16]byte(data[8:24]))
	destination := netip.AddrFrom16([16]byte(data[24:40]))
	packet := Packet{Direction: direction}
	if direction == model.DirectionInbound {
		packet.RemoteIP = source
	} else {
		packet.RemoteIP = destination
	}
	next := data[6]
	offset := 40
	firstFragment := true
	for extensions := 0; extensions < 8; extensions++ {
		switch next {
		case 0, 43, 60:
			if offset+2 > end {
				return Packet{}, errors.New("truncated IPv6 extension")
			}
			length := (int(data[offset+1]) + 1) * 8
			if length < 8 || offset+length > end {
				return Packet{}, errors.New("invalid IPv6 extension length")
			}
			next = data[offset]
			offset += length
			continue
		case 44:
			if offset+8 > end {
				return Packet{}, errors.New("truncated IPv6 fragment header")
			}
			fragment := binary.BigEndian.Uint16(data[offset+2 : offset+4])
			firstFragment = fragment&0xfff8 == 0
			next = data[offset]
			offset += 8
			if !firstFragment {
				// The fragment offset applies to the whole fragmentable part,
				// including any remaining extension headers. This payload need
				// not start at a header boundary, so never traverse it.
				decodeTransport(&packet, next, nil, false)
				return packet, nil
			}
			continue
		case 51:
			if offset+2 > end {
				return Packet{}, errors.New("truncated IPv6 AH header")
			}
			length := (int(data[offset+1]) + 2) * 4
			if length < 8 || offset+length > end {
				return Packet{}, errors.New("invalid IPv6 AH length")
			}
			next = data[offset]
			offset += length
			continue
		}
		break
	}
	if offset > end {
		return Packet{}, errors.New("invalid IPv6 transport offset")
	}
	decodeTransport(&packet, next, data[offset:end], firstFragment)
	return packet, nil
}

func decodeTransport(packet *Packet, protocol uint8, data []byte, parsePorts bool) {
	switch protocol {
	case 6:
		packet.Protocol = "tcp"
		if parsePorts && len(data) >= 14 {
			if packet.Direction == model.DirectionInbound {
				packet.LocalPort = binary.BigEndian.Uint16(data[2:4])
				packet.RemotePort = binary.BigEndian.Uint16(data[0:2])
			} else {
				packet.LocalPort = binary.BigEndian.Uint16(data[0:2])
				packet.RemotePort = binary.BigEndian.Uint16(data[2:4])
			}
			packet.TCPFlags = data[13]
		}
	case 17:
		packet.Protocol = "udp"
		if parsePorts && len(data) >= 4 {
			if packet.Direction == model.DirectionInbound {
				packet.LocalPort = binary.BigEndian.Uint16(data[2:4])
				packet.RemotePort = binary.BigEndian.Uint16(data[0:2])
			} else {
				packet.LocalPort = binary.BigEndian.Uint16(data[0:2])
				packet.RemotePort = binary.BigEndian.Uint16(data[2:4])
			}
		}
	case 1:
		packet.Protocol = "icmp"
	case 58:
		packet.Protocol = "icmpv6"
	default:
		packet.Protocol = "other"
	}
}

// SPDX-License-Identifier: MIT

//go:build !linux

package sensor

import (
	"context"
	"errors"
)

type Capture struct{}

func OpenCapture(string, int) (*Capture, error) {
	return nil, errors.New("AF_PACKET sensor is only available on Linux")
}
func (*Capture) Close() error { return nil }
func (*Capture) Stats() (uint64, uint64, error) {
	return 0, 0, errors.New("AF_PACKET sensor is only available on Linux")
}
func (*Capture) ParseErrors() uint64 { return 0 }
func (*Capture) Run(context.Context, chan<- Packet) error {
	return errors.New("AF_PACKET sensor is only available on Linux")
}

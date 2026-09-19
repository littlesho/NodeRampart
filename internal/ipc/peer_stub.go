// SPDX-License-Identifier: MIT

//go:build !linux

package ipc

import (
	"errors"
	"net"
)

type Peer struct {
	PID      int32
	UID, GID uint32
}

func PeerCredentials(*net.UnixConn) (Peer, error) {
	return Peer{}, errors.New("peer credentials are only supported on Linux")
}

// SPDX-License-Identifier: MIT

//go:build linux

package ipc

import (
	"fmt"
	"net"
	"syscall"
)

type Peer struct {
	PID int32
	UID uint32
	GID uint32
}

func PeerCredentials(connection *net.UnixConn) (Peer, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var peer Peer
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			socketErr = err
			return
		}
		peer = Peer{PID: credentials.Pid, UID: credentials.Uid, GID: credentials.Gid}
	}); err != nil {
		return Peer{}, err
	}
	if socketErr != nil {
		return Peer{}, fmt.Errorf("SO_PEERCRED: %w", socketErr)
	}
	return peer, nil
}

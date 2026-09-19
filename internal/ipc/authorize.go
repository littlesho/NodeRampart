// SPDX-License-Identifier: MIT

package ipc

import (
	"errors"
	"net"
	"os/user"
	"strconv"
	"time"
)

const (
	DaemonUser = "noderampart"
	SensorUser = "noderampart-sensor"
)

// ServiceUID resolves an installed service identity. Callers must explicitly
// opt into a manual current-UID mode; a missing account never weakens the check.
func ServiceUID(name string) (uint32, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return 0, errors.New("required service account is unavailable")
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return 0, errors.New("service account UID is invalid")
	}
	return uint32(uid), nil
}

func RequirePeerUID(connection *net.UnixConn, expected uint32) error {
	peer, err := PeerCredentials(connection)
	if err != nil {
		return errors.New("could not verify Unix peer credentials")
	}
	if peer.UID != expected {
		return errors.New("Unix peer UID does not match the required service account")
	}
	return nil
}

func DialUnixPeer(path string, timeout time.Duration, expected uint32) (*net.UnixConn, error) {
	connection, err := DialUnix(path, timeout)
	if err != nil {
		return nil, errors.New("daemon socket is unavailable")
	}
	if err := RequirePeerUID(connection, expected); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

// SPDX-License-Identifier: MIT

//go:build linux

package ipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDialUnixPeerRejectsWrongUIDBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.sock")
	listener, err := ListenUnix(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.UnixConn, 2)
	go func() {
		for i := 0; i < 2; i++ {
			connection, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			accepted <- connection
		}
	}()
	if connection, err := DialUnixPeer(path, time.Second, uint32(os.Geteuid())+1); err == nil {
		connection.Close()
		t.Fatal("incorrect peer UID accepted")
	}
	bad := <-accepted
	defer bad.Close()
	_ = bad.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if n, err := bad.Read(buffer); n != 0 || err == nil {
		t.Fatal("rejected connection received bytes")
	}
	good, err := DialUnixPeer(path, time.Second, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()
	peer := <-accepted
	defer peer.Close()
	if err := RequirePeerUID(peer, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
}

func TestMissingServiceUIDDoesNotFallback(t *testing.T) {
	if _, err := ServiceUID("noderampart-nonexistent-test-service-20260911"); err == nil {
		t.Fatal("missing service account must not fall back to current UID")
	}
}

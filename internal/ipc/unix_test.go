// SPDX-License-Identifier: MIT

//go:build linux

package ipc

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestUnixSocketPermissionsAndPeerCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.sock")
	listener, err := ListenUnix(path, 0o600)
	if errors.Is(err, syscall.EPERM) {
		t.Skip("Unix socket creation is blocked by the test sandbox")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("unexpected socket mode: %v", info.Mode())
	}
	peers := make(chan Peer, 1)
	errorsSeen := make(chan error, 1)
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			errorsSeen <- err
			return
		}
		defer connection.Close()
		peer, err := PeerCredentials(connection)
		if err != nil {
			errorsSeen <- err
			return
		}
		peers <- peer
	}()
	connection, err := DialUnix(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	select {
	case err := <-errorsSeen:
		t.Fatal(err)
	case peer := <-peers:
		if peer.UID != uint32(os.Geteuid()) {
			t.Fatalf("peer UID = %d, want %d", peer.UID, os.Geteuid())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for peer credentials")
	}
	if _, err := ListenUnix(path, 0o600); err == nil {
		t.Fatal("expected active socket replacement to be rejected")
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := ListenUnix(path, 0o600)
	if err != nil {
		t.Fatalf("replace stale socket: %v", err)
	}
	replacement.Close()
}

func TestListenUnixRefusesNonSocketTargets(t *testing.T) {
	directory := t.TempDir()
	regular := filepath.Join(directory, "regular")
	if err := os.WriteFile(regular, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(regular, 0o600); err == nil {
		t.Fatal("expected regular-file target to be rejected")
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(link, 0o600); err == nil {
		t.Fatal("expected symlink target to be rejected")
	}
}

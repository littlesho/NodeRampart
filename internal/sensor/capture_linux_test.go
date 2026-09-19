// SPDX-License-Identifier: MIT

//go:build linux

package sensor

import (
	"runtime"
	"sync"
	"testing"

	"github.com/littlesho/NodeRampart/internal/model"
)

func TestCaptureParseErrorsAreCountedWithoutChannelLoss(t *testing.T) {
	capture := &Capture{}
	for i := 0; i < 1024; i++ {
		if _, ok := capture.decodeFrame(nil, model.DirectionInbound); ok {
			t.Fatal("malformed packet decoded")
		}
	}
	if got := capture.ParseErrors(); got != 1024 {
		t.Fatalf("parse errors = %d, want 1024", got)
	}
	if got := capture.ParseErrors(); got != 0 {
		t.Fatalf("parse errors counted twice: %d", got)
	}
	unsupported := make([]byte, 14)
	unsupported[12], unsupported[13] = 0x08, 0x06 // ARP is unsupported, not malformed.
	if _, ok := capture.decodeFrame(unsupported, model.DirectionInbound); ok {
		t.Fatal("unsupported frame decoded")
	}
	if _, ok := capture.decodeFrame(makeIPv4UDP(t, "192.0.2.1"), model.DirectionInbound); !ok {
		t.Fatal("valid packet failed decoding")
	}
	if got := capture.ParseErrors(); got != 0 {
		t.Fatalf("valid/unsupported frames counted as errors: %d", got)
	}
}

func TestCaptureParseErrorsConcurrentFlush(t *testing.T) {
	capture := &Capture{}
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 5000; j++ {
				capture.decodeFrame(nil, model.DirectionInbound)
			}
		}()
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	var total uint64
	for {
		total += capture.ParseErrors()
		select {
		case <-done:
			total += capture.ParseErrors()
			if total != 20000 {
				t.Fatalf("concurrent parse total = %d, want 20000", total)
			}
			return
		default:
			runtime.Gosched()
		}
	}
}

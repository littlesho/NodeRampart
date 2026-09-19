// SPDX-License-Identifier: MIT

package protocol

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func FuzzReadFrame(f *testing.F) {
	var valid bytes.Buffer
	_ = WriteFrame(&valid, Batch{ProtocolVersion: Version, SentAt: time.Unix(1, 0).UTC(), IntervalMillis: 1000, Interface: "eth0"})
	f.Add(valid.Bytes())
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	for _, payload := range []string{`{"protocol_version":3,"unknown":true}`, `{} {}`, `{"flows":[{"remote_ip":"::ffff:192.0.2.1","packets":18446744073709551615}]}`} {
		frame := make([]byte, 4+len(payload))
		binary.BigEndian.PutUint32(frame, uint32(len(payload)))
		copy(frame[4:], payload)
		f.Add(frame)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > MaxFrameSize+4 {
			return
		}
		var got Batch
		if err := ReadFrame(bufio.NewReader(bytes.NewReader(raw)), &got); err != nil {
			return
		}
		_ = got.Validate() // Exercise bounded semantic checks on decoded input.
		var encoded bytes.Buffer
		if err := WriteFrame(&encoded, got); err != nil {
			return
		}
		var again Batch
		if err := ReadFrame(bufio.NewReader(&encoded), &again); err != nil {
			t.Fatalf("accepted frame cannot round-trip: %v", err)
		}
		first, _ := json.Marshal(got)
		second, _ := json.Marshal(again)
		if !reflect.DeepEqual(first, second) {
			t.Fatal("accepted frame changes during round-trip")
		}
	})
}

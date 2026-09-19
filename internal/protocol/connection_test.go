// SPDX-License-Identifier: MIT

package protocol

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestConnectionGenerationIsNeverAWireField(t *testing.T) {
	payload, err := json.Marshal(Batch{ConnectionID: 9001})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("9001")) {
		t.Fatal("local generation leaked to wire")
	}
	for _, key := range []string{"ConnectionID", "connection_id", "-"} {
		body := []byte(`{"` + key + `":9001}`)
		var frame bytes.Buffer
		if err := binary.Write(&frame, binary.BigEndian, uint32(len(body))); err != nil {
			t.Fatal(err)
		}
		frame.Write(body)
		var got Batch
		if err := ReadFrame(bufio.NewReader(&frame), &got); err == nil {
			t.Fatalf("wire injected %s", key)
		}
	}
}

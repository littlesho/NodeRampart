// SPDX-License-Identifier: MIT

package replay

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func FuzzReplayMetadata(f *testing.F) {
	f.Add([]byte(`{"format":"noderampart-replay","version":1,"anonymized":false,"interface_limit":1}` + "\n" + `{"type":"auth","auth":{"observed_at_utc":"2026-01-01T00:00:00Z","kind":"success","source_ip":"192.0.2.1","user":"synthetic","method":"publickey"}}` + "\n"))
	f.Add([]byte(`{"format":"noderampart-replay","version":1,"Version":2}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxRecordBytes+1024 {
			return
		}
		_, _ = anonymize(context.Background(), bytes.NewReader(data), io.Discard)
	})
}

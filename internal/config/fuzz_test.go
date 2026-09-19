// SPDX-License-Identifier: MIT

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func FuzzLoadConfig(f *testing.F) {
	defaults, _ := json.Marshal(Defaults())
	f.Add(defaults)
	f.Add([]byte(`{"schema_version":1}`))
	f.Add([]byte(`{"schema_version":1,"unknown":true}`))
	f.Add([]byte(`{"schema_version":1} {}`))
	f.Add([]byte(`{"schema_version":1,"auth":{"window":"999999999999999999999h"}}`))
	f.Add([]byte(`{"schema_version":1,"paths":{"database":"/tmp/../unsafe"}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxConfigSize+1 {
			return
		}
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			return
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("accepted configuration cannot encode: %v", err)
		}
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err != nil {
			t.Fatalf("accepted configuration cannot round-trip: %v", err)
		}
	})
}

// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyConfigReceivesStorageBudgetDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.MaxBytes != 1<<30 || cfg.Storage.MinFreeBytes != 128<<20 {
		t.Fatalf("missing safe defaults: %#v", cfg.Storage)
	}
}
func TestStorageBudgetConfigBounds(t *testing.T) {
	for _, maximum := range []int64{0, 64<<20 - 1, 64 << 20, 1 << 40, 1<<40 + 1} {
		cfg := Defaults()
		cfg.Storage.MaxBytes = maximum
		valid := maximum >= 64<<20 && maximum <= 1<<40
		if err := cfg.Validate(); (err == nil) != valid {
			t.Fatalf("max_bytes=%d valid=%v error=%v", maximum, valid, err)
		}
	}
	for _, minimum := range []int64{-1, 0, 128 << 20, 1 << 40, 1<<40 + 1} {
		cfg := Defaults()
		cfg.Storage.MinFreeBytes = minimum
		valid := minimum >= 0 && minimum <= 1<<40
		if err := cfg.Validate(); (err == nil) != valid {
			t.Fatalf("min_free_bytes=%d valid=%v error=%v", minimum, valid, err)
		}
	}
}

// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBillingCycleDefaultAndBounds(t *testing.T) {
	if Defaults().Billing.CycleStartDay != 1 {
		t.Fatal("cycle default is not day one")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"billing":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || cfg.Billing.CycleStartDay != 1 {
		t.Fatal("legacy config did not retain cycle default", err)
	}
	for _, day := range []int{0, -1, 29, 31} {
		cfg := Defaults()
		cfg.Billing.CycleStartDay = day
		if cfg.Validate() == nil {
			t.Fatalf("unsupported cycle day %d accepted", day)
		}
	}
	for _, day := range []int{1, 15, 28} {
		cfg := Defaults()
		cfg.Billing.CycleStartDay = day
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

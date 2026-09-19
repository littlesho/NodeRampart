// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestFlowCapacityMatchesProtocol(t *testing.T) {
	for _, capacity := range []int{63, 64, protocol.MaxFlowsPerBatch, protocol.MaxFlowsPerBatch + 1, 65536} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			cfg := Defaults()
			cfg.Sensor.MaxTrackedFlows = capacity
			valid := capacity >= 64 && capacity <= protocol.MaxFlowsPerBatch
			if err := cfg.Validate(); (err == nil) != valid {
				t.Fatalf("capacity %d: valid=%v, error=%v", capacity, valid, err)
			}
		})
	}
}

func TestDefaultsValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestLoadRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	link := filepath.Join(dir, "config.json")
	if err := os.WriteFile(target, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestLoadRejectsGroupWritableConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1}`), 0o660); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o660); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected group-writable configuration to be rejected")
	}
}

// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"testing"
)

func TestExplicitInterfaceSelectionBoundsAndCompatibility(t *testing.T) {
	for _, names := range [][]string{{"eth0", "eth1"}, {"eth0", "eth0"}, {"../bad"}, {""}} {
		cfg := Defaults()
		cfg.Sensor.Interfaces = names
		err := cfg.Validate()
		if (len(names) == 2 && names[1] == "eth1") != (err == nil) {
			t.Fatalf("unexpected selection validation: %v %v", names, err)
		}
	}
	cfg := Defaults()
	cfg.Sensor.Interface = "eth0"
	if cfg.Sensor.InterfaceLimit() != 1 || len(cfg.Sensor.InterfaceNames()) != 1 {
		t.Fatal("legacy interface contract lost")
	}
	cfg.Sensor.Interfaces = []string{"eth1"}
	if cfg.Validate() == nil {
		t.Fatal("ambiguous interface configuration accepted")
	}
	cfg = Defaults()
	for i := range 9 {
		cfg.Sensor.Interfaces = append(cfg.Sensor.Interfaces, fmt.Sprintf("eth%d", i))
	}
	if cfg.Validate() == nil {
		t.Fatal("interface cap bypassed")
	}
	if Defaults().Sensor.InterfaceLimit() != 2 {
		t.Fatal("automatic dual-stack budget must be finite")
	}
}

// SPDX-License-Identifier: MIT

package manage

import (
	"os"
	"testing"

	"github.com/littlesho/NodeRampart/internal/assets"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[1] == "assets" && os.Args[2] == "validate-mmdb" {
		if err := assets.ValidatorCommand(os.Args[3:], os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

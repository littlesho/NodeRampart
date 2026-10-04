// SPDX-License-Identifier: MIT
//go:build !linux

package collector

import "os"

func journalExitSignal(*os.ProcessState) string { return "" }

// SPDX-License-Identifier: MIT
//go:build !linux

package collector

import (
	"context"
	"errors"
)

func defaultInterfaces(context.Context) ([]InterfaceRef, error) {
	return nil, errors.New("automatic route discovery requires Linux")
}

//go:build !linux

package ble

import (
	"errors"
	"log/slog"
)

// NewPlatformBackend is unsupported outside Linux for now.
func NewPlatformBackend(*slog.Logger) (Backend, error) {
	return nil, errors.New("only Linux (BlueZ) is supported")
}

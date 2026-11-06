// Package ble wraps tinygo.org/x/bluetooth and exposes the interfaces
// the runtime needs.
package ble

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Advert is a BLE advertisement observed during a scan.
type Advert struct {
	ID       string
	Name     string
	RSSI     int
	Services []string
}

// Backend abstracts the BLE adapter.
type Backend interface {
	Scan(ctx context.Context, fn func(Advert)) error
	Connect(id string) (Peripheral, error)
}

// Peripheral represents an active GATT connection.
type Peripheral interface {
	Services() []string
	Characteristic(service, char string) (Characteristic, bool)
	Connected() bool
	Disconnect() error
}

// Characteristic exposes read, write and notify.
type Characteristic interface {
	Read() ([]byte, error)
	Write(data []byte, withResponse bool) error
	Notify(fn func([]byte)) error
}

// WeDoServiceUUID is the WeDo 2.0 service (§16.3).
const WeDoServiceUUID = "00001523-1212-efde-1523-785feabcd123"

const baseUUIDSuffix = "-0000-1000-8000-00805f9b34fb"

// NormalizeUUID accepts a short UUID (e.g. "0x1523", "1523") or a full
// UUID and returns the canonical 128-bit form.
func NormalizeUUID(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "0x")
	if !strings.Contains(s, "-") && len(s) <= 8 {
		n, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			return "", fmt.Errorf("invalid UUID %q", s)
		}
		return fmt.Sprintf("%08x%s", n, baseUUIDSuffix), nil
	}
	c := strings.ReplaceAll(s, "-", "")
	if _, err := hex.DecodeString(c); err != nil || len(c) != 32 {
		return "", fmt.Errorf("invalid UUID %q", s)
	}
	return c[:8] + "-" + c[8:12] + "-" + c[12:16] + "-" + c[16:20] + "-" + c[20:], nil
}

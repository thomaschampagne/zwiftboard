//go:build darwin

package ble

import (
	"errors"

	"tinygo.org/x/bluetooth"
)

// NewAddress is unsupported on macOS: the CoreBluetooth backend addresses
// peripherals by a UUID that macOS assigns per central (adapter_darwin.go), so
// it cannot be derived from a MAC address. -addr is a Windows scanning
// shortcut that skips the watch loop.
func NewAddress(mac string) (bluetooth.Address, error) {
	return bluetooth.Address{}, errors.New("-addr is not supported on macOS (peripheral addresses are per-central UUIDs, not MACs)")
}

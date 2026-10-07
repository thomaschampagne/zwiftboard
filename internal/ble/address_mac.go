//go:build !darwin

package ble

import "tinygo.org/x/bluetooth"

// NewAddress parses a BLE MAC string into an address.
//
// Excluded on macOS: Darwin identifies peripherals by a per-central UUID
// (adapter_darwin.go), so its bluetooth.Address has no MACAddress field.
func NewAddress(mac string) (bluetooth.Address, error) {
	m, err := bluetooth.ParseMAC(mac)
	if err != nil {
		return bluetooth.Address{}, err
	}
	return bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: m}}, nil
}

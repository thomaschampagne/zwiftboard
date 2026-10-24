//go:build !darwin

package ble

import (
	"runtime"
	"tinygo.org/x/bluetooth"
)

func sessionAlive(bluetooth.Device) error { return nil }

// Preserve the WinRT write probe: Disconnect on a freed GATT session can AV.
func teardownSession(where string, probe func() error, disconnect func()) {
	endSession(where, probe, disconnect)
}

func BluetoothHelp() string {
	if runtime.GOOS == "windows" {
		return "Turn Bluetooth on in Settings > Bluetooth & devices; retrying in 5s."
	}
	return "Turn Bluetooth on and wake both controllers by pressing a button."
}

func validateConnectedDevice(bluetooth.Device, bluetooth.Address) error { return nil }

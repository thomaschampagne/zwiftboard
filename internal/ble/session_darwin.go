package ble

import (
	"errors"
	"tinygo.org/x/bluetooth"
)

func sessionAlive(dev bluetooth.Device) error {
	return connectionState(dev.Connected)
}

func connectionState(connected func() (bool, error)) error {
	up, err := connected()
	if err != nil {
		return err
	}
	if !up {
		return errors.New("CoreBluetooth peripheral disconnected")
	}
	return nil
}

// CoreBluetooth cancellation is safe even after a disconnect, and must also
// run when discovery failed before a writable characteristic was available.
func teardownSession(_ string, _ func() error, disconnect func()) { disconnect() }

// BluetoothHelp describes the platform's startup recovery steps.
func BluetoothHelp() string {
	return "Turn Bluetooth on in System Settings; allow Bluetooth access for your terminal or zwiftboard in Privacy & Security > Bluetooth."
}

// TinyGo can return Device{}, nil if the peripheral disconnects before its
// connection callback is handled. Validate identity before touching its nil
// native handle, then check state before service discovery.
func validateConnectedDevice(dev bluetooth.Device, expected bluetooth.Address) error {
	return checkConnection(dev.Address.String(), expected.String(), dev.Connected, func() { _ = dev.Disconnect() })
}

func checkConnection(actual, expected string, state func() (bool, error), cancel func()) error {
	if actual != expected || actual == "00000000-0000-0000-0000-000000000000" {
		return errors.New("CoreBluetooth returned no matching connected peripheral")
	}
	if err := connectionState(state); err != nil {
		cancel()
		return err
	}
	return nil
}

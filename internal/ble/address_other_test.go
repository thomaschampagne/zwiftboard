//go:build !darwin

package ble

import "tinygo.org/x/bluetooth"

func testAddress(value string) bluetooth.Address {
	var a bluetooth.Address
	a.Set(value)
	return a
}

//go:build !darwin

package ble

import "tinygo.org/x/bluetooth"

func testAddress(seed string) bluetooth.Address {
	mac, err := bluetooth.ParseMAC(seed)
	if err != nil {
		panic(err)
	}
	return bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: mac}}
}

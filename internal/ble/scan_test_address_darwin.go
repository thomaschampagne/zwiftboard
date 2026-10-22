//go:build darwin

package ble

import (
	"crypto/sha1"
	"fmt"

	"tinygo.org/x/bluetooth"
)

func testAddress(seed string) bluetooth.Address {
	sum := sha1.Sum([]byte(seed))
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	parsed, err := bluetooth.ParseUUID(uuid)
	if err != nil {
		panic(err)
	}
	return bluetooth.Address{UUID: parsed}
}

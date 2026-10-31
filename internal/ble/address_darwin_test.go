package ble

import (
	"strings"
	"testing"
	"time"
	"tinygo.org/x/bluetooth"
)

// Keep the shared scan cases readable while representing CoreBluetooth's
// peripheral identity as a UUID, never as a fabricated MACAddress field.
func testAddress(value string) bluetooth.Address {
	var a bluetooth.Address
	a.Set("12345678-1234-5678-9abc-" + strings.ReplaceAll(value, ":", ""))
	return a
}

func TestScanPreservesDarwinPeripheralUUID(t *testing.T) {
	const uuid = "12345678-1234-5678-9abc-d4060fa98604"
	stubScan(t, func(cb scanCallback) error { cb(nil, zwiftResult("D4:06:0F:A9:86:04")); return nil })
	pending, order, err := scanBurst(time.Second, func(string) bool { return false })
	if err != nil || len(order) != 1 || order[0] != uuid || pending[uuid].Addr.String() != uuid {
		t.Fatalf("UUID identity lost: pending=%v order=%v err=%v", pending, order, err)
	}
	if !SeenRecently(uuid, time.Second) {
		t.Fatal("UUID not recorded for reconnect")
	}
	ClearSighting(uuid)
	if SeenRecently(uuid, time.Second) {
		t.Fatal("UUID reconnect gate not cleared")
	}
}

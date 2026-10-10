// Package zwift holds the Click V2 protocol facts shared by the BLE and
// config layers: GATT UUIDs, button bits, frame decoding and small helpers.
//
// Protocol notes (Click V2):
//   - activation: write "RideOn"+02 03, then 00 08 00, then 00 08 10 (100ms apart);
//     unlocked device echoes "RideOn" on SyncTX, a locked one sends a 0xFF challenge
//   - keepalive: re-send the raw "RideOn" opcode frame every 3s; this stops the
//     pod's ~1 min deep-sleep. "RideOn"+02 03 and 00 08 10 (alone or combined)
//     did not hold the link on Windows
//   - button frames: 0x23 + protobuf; field 1 = uint32 bitmap, bit==0 means pressed
//   - LEFT needs a ~24h hardware unlock done by the Zwift app; "ff 04 00" keeps an
//     already-unlocked device unlocked
package zwift

import (
	"strings"

	"tinygo.org/x/bluetooth"
)

const (
	// CompanyID is Zwift's Bluetooth SIG company identifier.
	CompanyID = 0x094A

	// Service UUIDs (Zwift custom service, same suffix for all).
	ServiceUUID = "00000001-19ca-4651-86e5-fa29dcdd09d1"
	AsyncUUID   = "00000002-19ca-4651-86e5-fa29dcdd09d1" // notify: key presses
	SyncRXUUID  = "00000003-19ca-4651-86e5-fa29dcdd09d1" // write: handshake / keepalive
	SyncTXUUID  = "00000004-19ca-4651-86e5-fa29dcdd09d1" // indicate: handshake reply

	// MsgKeyPad is the frame type carrying the button bitmap.
	MsgKeyPad = 0x23

	// Pod device-type bytes (manufacturer data first byte, company 0x094A).
	// The Click V2 is a PAIR and the side is encoded in the advertisement:
	// 0x0B = LEFT pod (the pair's BLE anchor), 0x0A = RIGHT pod.
	PodRightDeviceID = 0x0A
	PodLeftDeviceID  = 0x0B
)

// Pod is one side of the Click V2 pod pair (both are connected; see AGENTS.md).
type Pod int

const (
	PodUnknown Pod = iota
	PodLeft
	PodRight
)

// String names a pod for labels and log messages.
func (p Pod) String() string {
	switch p {
	case PodLeft:
		return "left"
	case PodRight:
		return "right"
	default:
		return "unknown"
	}
}

// PodSide maps a Zwift manufacturer-data device byte to its pod side, so the
// scanner can tell which pod it found without connecting.
func PodSide(id byte) Pod {
	switch id {
	case PodLeftDeviceID:
		return PodLeft
	case PodRightDeviceID:
		return PodRight
	default:
		return PodUnknown
	}
}

// Buttons maps Click V2 button bits to names. Bit == 0 in the frame means
// pressed (the full Zwift Ride layout does not apply).
var Buttons = map[uint32]string{
	0x00001: "LEFT",  // left module, arrow left
	0x00002: "UP",    // left module, arrow up
	0x00004: "RIGHT", // left module, arrow right
	0x00008: "DOWN",  // left module, arrow down
	0x00010: "A",
	0x00020: "B",
	0x00040: "Y",
	0x00080: "Z",
	0x00100: "MIN",  // left module, minus
	0x01000: "PLUS", // right module, plus
}

// KnownMask is the union of all defined button bits.
var KnownMask = func() (m uint32) {
	for k := range Buttons {
		m |= k
	}
	return
}()

// DeviceID returns the Zwift device id from manufacturer data
// (company 0x094A, first data byte = device ID).
func DeviceID(r bluetooth.ScanResult) (byte, bool) {
	for _, m := range r.ManufacturerData() {
		if m.CompanyID == CompanyID && len(m.Data) > 0 {
			return m.Data[0], true
		}
	}
	return 0, false
}

// IsZwift reports whether a scan result looks like a Zwift controller:
// manufacturer data match or a "zwift…" local name.
func IsZwift(r bluetooth.ScanResult) (deviceID byte, ok bool) {
	id, match := DeviceID(r)
	if match {
		return id, true
	}
	if strings.HasPrefix(strings.ToLower(r.LocalName()), "zwift") {
		return 0, true
	}
	return 0, false
}

// Bitmap extracts protobuf field 1 (varint) = key bitmap from frame payload,
// skipping other fields.
func Bitmap(p []byte) (uint32, bool) {
	for i := 0; i < len(p); {
		tag, n := uvarint(p[i:])
		if n == 0 {
			return 0, false
		}
		i += n
		num, wt := tag>>3, tag&7
		switch wt {
		case 0:
			v, n := uvarint(p[i:])
			if n == 0 {
				return 0, false
			}
			i += n
			if num == 1 {
				return uint32(v), true
			}
		case 1:
			i += 8
		case 2:
			l, n := uvarint(p[i:])
			if n == 0 {
				return 0, false
			}
			i += n + int(l)
		case 5:
			i += 4
		default:
			return 0, false
		}
	}
	return 0, false
}

func uvarint(b []byte) (uint64, int) {
	var x uint64
	for i, c := range b {
		if i == 10 {
			return 0, 0
		}
		x |= uint64(c&0x7f) << (7 * uint(i))
		if c < 0x80 {
			return x, i + 1
		}
	}
	return 0, 0
}

// ShortName is a compact device name for labels.
func ShortName(n string) string {
	if n == "" {
		return "zwift"
	}
	return strings.ReplaceAll(n, " ", "")
}

// Tail is the last 5 chars of a BLE address (86:04) for compact labels.
func Tail(addr string) string {
	if len(addr) > 5 {
		return addr[len(addr)-5:]
	}
	return addr
}

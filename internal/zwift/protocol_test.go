package zwift

import "testing"

func TestPodSide(t *testing.T) {
	cases := []struct {
		id   byte
		want Pod
	}{
		{PodRightDeviceID, PodRight},
		{PodLeftDeviceID, PodLeft},
		{0x09, PodUnknown}, // legacy Click (BC1), not a Click V2 pod
		{0x00, PodUnknown}, // no manufacturer data / id bytes
		{0xFF, PodUnknown},
	}
	for _, c := range cases {
		if got := PodSide(c.id); got != c.want {
			t.Errorf("PodSide(0x%02X) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestPodString(t *testing.T) {
	cases := []struct {
		p    Pod
		want string
	}{
		{PodLeft, "left"},
		{PodRight, "right"},
		{PodUnknown, "unknown"},
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Errorf("(%v).String() = %q, want %q", c.p, got, c.want)
		}
	}
}

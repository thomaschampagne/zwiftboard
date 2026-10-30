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

// The pod button lists and the bit map must agree exactly: a bit added to
// one and forgotten in the other would silently drop a button.
func TestPodButtonListsMatchBitMap(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range append(append([]string{}, LeftButtons...), RightButtons...) {
		if seen[n] {
			t.Errorf("button %q is listed twice", n)
		}
		seen[n] = true
	}
	if len(seen) != len(Buttons) {
		t.Errorf("the pod lists cover %d buttons, the bit map has %d", len(seen), len(Buttons))
	}
	for bit, name := range Buttons {
		if !seen[name] {
			t.Errorf("bit 0x%x (%s) is in the bit map but in no pod list", bit, name)
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

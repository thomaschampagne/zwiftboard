//go:build !darwin

package keys

import "testing"

func TestResolveInsertPreserved(t *testing.T) {
	b, err := Resolve("insert")
	if err != nil || b.VK != 0x2d {
		t.Fatalf("Resolve(insert) = %+v, %v", b, err)
	}
}

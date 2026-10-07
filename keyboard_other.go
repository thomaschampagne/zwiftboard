//go:build !windows

package main

// keyTap is a no-op off Windows; BLE support here is Windows-only anyway.
func keyTap(b keyBinding) {}

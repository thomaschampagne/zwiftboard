//go:build windows

package main

import (
	"log"

	"golang.org/x/sys/windows"
)

var (
	user32         = windows.NewLazySystemDLL("user32.dll")
	procKeybdEvent = user32.NewProc("keybd_event")
)

const keyEventKeyUp = 0x0002 // KEYEVENTF_KEYUP

// keyTap presses and releases a virtual key via keybd_event.
func keyTap(b keyBinding) {
	procKeybdEvent.Call(uintptr(b.vk), 0, 0, 0)
	procKeybdEvent.Call(uintptr(b.vk), 0, keyEventKeyUp, 0)
	if verbose {
		log.Printf("key %s (vk %#02x)", b.token, b.vk)
	}
}

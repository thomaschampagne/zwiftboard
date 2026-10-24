//go:build darwin && cgo

package main

/*
// A CLI has no app bundle. Embed the privacy description in its Mach-O image
// so CoreBluetooth can identify why it needs access. This also travels with
// go build/go run binaries, rather than relying on an optional linker flag.
__attribute__((used, section("__TEXT,__info_plist")))
static const char zwiftboard_info_plist[] =
"<?xml version=\"1.0\" encoding=\"UTF-8\"?>"
"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">"
"<plist version=\"1.0\"><dict>"
"<key>CFBundleIdentifier</key><string>com.github.thomaschampagne.zwiftboard</string>"
"<key>CFBundleName</key><string>zwiftboard</string>"
"<key>NSBluetoothAlwaysUsageDescription</key>"
"<string>Connect to Zwift Click V2 controllers to turn button presses into keyboard input.</string>"
"</dict></plist>";
*/
import "C"

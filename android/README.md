# ZwiftBoard Android (experimental)

This native Android app scans for both Zwift Click V2 pods, keeps GATT sessions
active in a foreground service, and forwards right-pod steering and shifting
events to an experimental MyWhoosh Link TCP server.

## Run

Open the `android` directory in Android Studio, allow the Gradle sync to finish,
and run the `app` configuration on an Android 8.0+ tablet with BLE. Or build
from PowerShell at the repository root:

```powershell
.\android\gradlew.bat -p android testDebugUnitTest assembleDebug
```

The APK is written to
`android\app\build\outputs\apk\debug\app-debug.apk`. Tap **Start background
bridge**, grant Bluetooth permissions, and turn on both Click pods. The ongoing
notification indicates that the foreground service is active. The app must be
granted Bluetooth access and must not be battery-restricted by the tablet
vendor.

The starter mapping is:

| Click V2 button | MyWhoosh control |
| --- | --- |
| `A` | Steer right while pressed |
| `Z` | Steer left while pressed |
| `B` | Shift up |
| `PLUS` | Shift down |

`LEFT` and `RIGHT` are kept connected because the Click V2 is a paired device;
only the right pod's mapped buttons send controls.

## MyWhoosh Link status

The app listens on TCP port `21587`, sends the observed `ConnectedDevicesList`
handshake when MyWhoosh connects, and sends JSON `Controls` messages. This
implements the public protocol observations in
[`shotah/zwift-to-mywhoosh-rust`](https://github.com/shotah/zwift-to-mywhoosh-rust/blob/main/troubleshooting/MYWHOOSH_LINK_PROTOCOL.md).

**Endpoint discovery/registration is not implemented or verified.** MyWhoosh
may need to register the Link endpoint through its official Link app/backend
before it connects to this server. The UI can show that the TCP server is
listening, but that does not mean MyWhoosh has connected. The Link app must not
be running at the same time if it owns the same port or endpoint.

## Protocol and hardware caveats

- Both pods must be available to this app; close Zwift, Companion, or any other
  app that already has a BLE connection to either pod.
- The LEFT pod may require the unlock performed through the official Zwift app.
- The app uses the known Click V2 handshake, 3-second keepalive, and 55-second
  idle reset behavior from the Windows project. The Android BLE lifecycle and
  idle-reset behavior still require verification on real hardware.
- The app does not emulate Android keyboard input or use Accessibility. It
  sends MyWhoosh Link control messages.

## Tests

Run `.\gradlew.bat testDebugUnitTest` from the `android` directory. Unit tests
cover Click button-frame decoding and the MyWhoosh control-message mapping.

#!/usr/bin/env python3
"""Validate the embedded CLI privacy plist in a thin 64-bit Mach-O binary."""
import pathlib
import plistlib
import struct
import sys

binary = pathlib.Path(sys.argv[1]).read_bytes()
if binary[:4] != b'\xcf\xfa\xed\xfe':
    raise SystemExit('expected a little-endian 64-bit Mach-O binary')
commands = struct.unpack_from('<I', binary, 16)[0]
pos = 32
for _ in range(commands):
    command, size = struct.unpack_from('<II', binary, pos)
    if command == 0x19:  # LC_SEGMENT_64
        sections = struct.unpack_from('<I', binary, pos + 64)[0]
        for index in range(sections):
            section = pos + 72 + index * 80
            name = binary[section:section + 16].rstrip(b'\0')
            segment = binary[section + 16:section + 32].rstrip(b'\0')
            if name == b'__info_plist' and segment == b'__TEXT':
                length, offset = struct.unpack_from('<QI', binary, section + 40)
                plist = plistlib.loads(binary[offset:offset + length].rstrip(b'\0'))
                if not plist.get('NSBluetoothAlwaysUsageDescription'):
                    raise SystemExit('Bluetooth usage description missing')
                print(plist['NSBluetoothAlwaysUsageDescription'])
                sys.exit(0)
    pos += size
raise SystemExit('__TEXT,__info_plist section missing')

# Controller templates

License: AGPL-3.0-or-later, see LICENSE

These files preset the controller mapping in the image, so the controllers below work in
EmulationStation and RetroArch on the first boot without the input wizard (spec section 5.5).
Controllers that are not listed here (or that report other IDs) still get the ES wizard
"Configure Input" at start.

`provision/common/app-50-controllers.sh` reads every `*.xml` here and then every `*.xml` in
`controllers/custom/` (exports from the web UI). For the same GUID the later node wins, so
`custom/` wins over `templates/`. For each node it writes `es_temporaryinput.cfg`, runs RetroPie's
`inputconfiguration.sh` as user `emulationstation` (this writes the RetroArch autoconfig in
`/opt/retropie/configs/all/retroarch-joypads/<deviceName>.cfg`) and then puts the node into
`es_input.cfg`. A keyboard node is never added.

## File format

Each file is an `<inputList>` with `<inputConfig type="joystick">` nodes, in the format ES writes:

- Two nodes per device: the SDL GUID with the CRC of the kernel name (SDL 2.26 and newer, as on
  Debian 12 bookworm) and the GUID without CRC (older SDL). The values are in the
  `guid-source:` lines of the comment; the GUIDs are the output of
  `emulationstation-web guid --bus B --vendor V --product P --version N --name NAME [--crc]`.
  `test/bats/controllers.bats` checks this.
- `deviceName` is the SDL joystick name (what ES writes and uses as a fallback match when the
  GUID differs). SDL has own names for Xbox pads (for example "Xbox 360 Controller"); for Sony pads
  the SDL name is the kernel name.
- `vendorId`/`productId` (decimal, as ES writes them): RetroArch then also matches its autoconfig
  by vendor and product ID, because RetroArch sees the kernel name, not the SDL name.
- All 25 inputs of the ES wizard. Positions as in RetroPie: ES `a` = right button (east),
  `b` = bottom (south), `x` = top (north), `y` = left (west); `hotkeyenable` = Select/Back/Share;
  D-pad = hat 0 (up 1, right 2, down 4, left 8); left stick axes 0/1, right stick 3/4,
  triggers axes 2/5.

## Devices

ES button IDs are SDL joystick indexes: the evdev key codes of the device in ascending order
(`BTN_JOYSTICK` and up first, see SDL `linux/SDL_sysjoystick.c`). ES disables the SDL HIDAPI
drivers, so SDL always uses the kernel (evdev) device.

| File | Bus | Kernel driver | IDs SDL sees (vendor:product, version) | Kernel name | south/east/west/north, L/R, Select/Start | Triggers |
|---|---|---|---|---|---|---|
| `xbox360-usb.xml` (also 8BitDo X-Input) | USB | xpad | 045e:028e, 0x0114 | Microsoft X-Box 360 pad | 0/1/2/3, 4/5, 6/7 | axes 2/5 |
| `xboxone-usb.xml` | USB | xpad | 045e:02ea, 0x0517 | Microsoft X-Box One S pad | 0/1/2/3, 4/5, 6/7 | axes 2/5 |
| `xboxseries-usb.xml` | USB | xpad | 045e:0b12, 0x0517 | Microsoft Xbox Series S\|X Controller | 0/1/2/3, 4/5, 6/7 | axes 2/5 |
| `xboxone-bt.xml` | BT | xpadneo | 045e:028e, 0x1130 (real ID 02fd) | Xbox Wireless Controller | 0/1/2/3, 4/5, 6/7 | axes 2/5 |
| `xboxseries-bt.xml` | BT | xpadneo | 045e:028e, 0x1130 (real ID 0b13) | Xbox Wireless Controller | 0/1/2/3, 4/5, 6/7 | axes 2/5 |
| `ds4-usb.xml` | USB | hid-playstation | 054c:09cc and 054c:05c4, 0x8111 | Sony Interactive Entertainment Wireless Controller / Sony Computer Entertainment Wireless Controller | 0/1/3/2, 4/5, 8/9 | axes 2/5 |
| `ds4-bt.xml` | BT | hid-playstation | 054c:09cc and 054c:05c4, 0x8100 | Wireless Controller | 0/1/3/2, 4/5, 8/9 | axes 2/5 |
| `dualsense-usb.xml` | USB | hid-playstation | 054c:0ce6, 0x8111 | Sony Interactive Entertainment DualSense Wireless Controller | 0/1/3/2, 4/5, 8/9 | axes 2/5 |
| `dualsense-bt.xml` | BT | hid-playstation | 054c:0ce6, 0x8100 | DualSense Wireless Controller | 0/1/3/2, 4/5, 8/9 | axes 2/5 |

Findings that differ from the first plan:

- xpadneo v0.10.5 reports every supported controller as 045e:028e with version 0x1130
  (`core.c`). So the Bluetooth templates use these IDs, not 02fd/0b13, and `xboxone-bt.xml` and
  `xboxseries-bt.xml` have the same nodes. If xpadneo is missing (DKMS failed, see the build
  report), hid-generic handles the pad with its real ID (02fd/0b13) and other button codes.
  The ES wizard does NOT appear: SDL names these IDs "Xbox One S Controller" and
  "Xbox Series X Controller" (controller_list.h), the same deviceName as in
  `xboxone-usb.xml` and `xboxseries-usb.xml`. The GUID does not match, so ES matches by name and
  loads the USB mapping onto the hid-generic layout: the buttons are wrong (spec section 9,
  xpadneo build fails). The "reset" on the web UI controller page does not help: it matches the
  GUID or the kernel name "Xbox Wireless Controller", and es_input.cfg has neither. Fix: connect a
  USB keyboard (ES has a built-in default keyboard mapping) and use the ES menu "Configure Input"
  for the pad; or fix xpadneo (see the build report) and rebuild. Another way: via SSH remove the
  `<inputConfig>` node with that deviceName/GUID from `~/.emulationstation/es_input.cfg` and
  restart ES.
- On the image kernel (Raspberry Pi 6.18) the DualShock 4 uses hid-playstation (since Linux 6.2),
  so it has the same button codes as the DualSense. hid-playstation adds 0x8000 to the version.
- USB HID devices (DualShock 4, DualSense) report bcdHID (0x0111) as version, not bcdDevice.
- For Xbox USB pads the version is the bcdDevice, which changes with the controller firmware.
  When it differs, ES finds the node by the SDL name instead.

Each XML file lists what could not be checked without hardware ("UNVERIFIED - check on
hardware"). The main points: the version of Xbox One/Series pads over USB, the Bluetooth names,
the DS4 v1 USB name, and that ES on bookworm writes the SDL names.

## Sources

- xpad: https://github.com/raspberrypi/linux/blob/rpi-6.18.y/drivers/input/joystick/xpad.c
- hid-playstation: https://github.com/raspberrypi/linux/blob/rpi-6.18.y/drivers/hid/hid-playstation.c
- usbhid (version = bcdHID): https://github.com/raspberrypi/linux/blob/rpi-6.18.y/drivers/hid/usbhid/hid-core.c
- hidp (Bluetooth name and version): https://github.com/raspberrypi/linux/blob/rpi-6.18.y/net/bluetooth/hidp/core.c
- xpadneo v0.10.5: https://github.com/atar-axis/xpadneo/tree/v0.10.5/hid-xpadneo/src/xpadneo
- SDL 2.26.5 (Debian 12 bookworm libsdl2): https://github.com/libsdl-org/SDL/tree/release-2.26.5/src/joystick
- SDL 3.2.10 (the names were first checked here): https://github.com/libsdl-org/SDL/tree/release-3.2.10/src/joystick
- SDL_GameControllerDB: https://github.com/mdqinc/SDL_GameControllerDB
- RetroPie EmulationStation (HIDAPI off, GUID then name match, vendorId/productId):
  https://github.com/RetroPie/EmulationStation/blob/stable/es-core/src/InputManager.cpp and
  `InputConfig.cpp`
- RetroPie-Setup f5ebfba6 (`inputconfiguration.sh`, `configscripts/retroarch.sh`,
  `configscripts/emulationstation.sh`):
  https://github.com/RetroPie/RetroPie-Setup/tree/f5ebfba638179631d4619f2b52075dec33d4bca8/scriptmodules/supplementary/emulationstation

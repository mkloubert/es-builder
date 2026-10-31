# EmuStation Builder

License: AGPL-3.0-or-later, see LICENSE

EmuStation Builder makes a ready-to-use retro gaming system. It builds a disk image with
Debian 12 ("bookworm"), EmulationStation (RetroPie) with the EpicNoir theme, many emulators, and a
web page to manage the system.

- `rpi4`: image for the Raspberry Pi 4 (2 GB). It boots straight into EmulationStation.
- `vm-amd64` and `vm-arm64`: UEFI images for QEMU, UTM or VirtualBox. They are for testing the web
  UI, SSH and the firewall. Playing games in the VM is not a goal.

What you get:

- EmulationStation with the EpicNoir theme, and emulators for many systems (NES to Dreamcast, arcade, home computers, DOS).
- A web UI at `https://<device>:11944` to upload ROMs, rename and scrape games, set up controllers,
  pair Bluetooth pads, restart or stop EmulationStation, and update the system.
- SSH on port `11911`. A firewall that opens only 11911 and 11944. fail2ban bans an address for
  one hour after 5 failed logins.

Source code: <https://github.com/mkloubert/es-builder>

## Requirements

- A Linux or macOS computer (amd64 or arm64).
  - macOS: Docker Desktop, OrbStack or Colima.
  - Linux: Docker.
- Free disk space for Docker: about 40 GB for `rpi4`, 20 GB for one VM image, 80 GB for `all`.
- A foreign CPU architecture needs binfmt/QEMU user emulation. `build.sh` sets it up for you
  (it starts the `tonistiigi/binfmt` container). Docker must be allowed to run privileged containers.
- Internet access during the build.

**Build time.** On the Pi, most emulators come as prebuilt RetroPie binaries, so the first Pi
image is much faster than a build from source. The VM images still build their few emulators from
source. On an amd64 computer the arm64 code runs under emulation, which is slower. The result of
this step is stored in a Docker volume (the base layer cache). Later builds reuse it and are much
faster. The cache changes only if the base scripts or the version pins change, or
if you use `--rebuild-base`.

## Set up `build.conf`

```
cp build.conf.example build.conf
```

Edit `build.conf`. The file is plain `KEY=VALUE`. Values are not executed.

- `USER_PASSWORD` is **required**. It is the password of the Linux user `emulationstation`
  (SSH, console, `sudo`).
- `USER_SSH_PUBKEY` (optional): your public SSH key, so you can log in without a password.
- `HOSTNAME`: the device name. Default `emulationstation`.
- `ES_AUTOSTART`: `yes` or `no`. Default **`no`**. With `yes`, the device boots straight into
  EmulationStation. With `no`, the screen (tty1) logs in user `emulationstation` by itself and
  shows a shell with a hint. To start EmulationStation there, type
  `exec /usr/local/lib/emulationstation/es-session`. While it does not run, the web UI shows
  "EmulationStation is off". It cannot start it, but all other pages work. Any other value
  stops the build.
  **Note:** older images always booted into EmulationStation. If you want that, set
  `ES_AUTOSTART=yes` before you build.
- `WEB_USER` and `WEB_PASSWORD`: login for the web UI. The defaults are `zocken` / `zocken`.
  Change them (here or later, see "Change the default access").
- `TLS_EXTRA_SANS` (optional): more names or IP addresses for the HTTPS certificate, separated by
  commas. Example: `emulationstation.fritz.box,192.168.178.50`. The certificate is valid for
  `HOSTNAME`, `HOSTNAME.local` and these entries. If you open the web UI under another name, the
  browser shows a warning.
  Note: `HOSTNAME.local` does **not** work on your network. The image has no mDNS (Avahi is
  removed on purpose). Open the web UI with the IP address of the device, or with the name your
  router gives it (for example `emulationstation.fritz.box`). Put that name or a fixed IP address
  in `TLS_EXTRA_SANS`, so the certificate fits.
- `SOURCE_URL`: the link to the source code in the footer of the web UI (AGPL-3.0, section 13).
  The default is this repository. If you publish a changed version, enter the URL of your fork.
  Empty means no link.
- `WIFI_SSID`, `WIFI_PASSWORD`, `WIFI_COUNTRY` (optional): Wi-Fi. Empty means LAN with DHCP.
  The image connects to this network at boot (WPA2/WPA3 personal; open networks are not supported).
  On the Raspberry Pi, `WIFI_COUNTRY` matters: Raspberry Pi OS blocks Wi-Fi until a country is set.
  Use the two-letter code of your country (for example `DE`, `US`, `GB`).
- `SCREENSCRAPER_USER` and `SCREENSCRAPER_PASSWORD` (optional): account for the scraper.
- `LOCALE`, `TIMEZONE`, `KEYMAP`: system settings. The defaults are German.
- The "Upstream pins" at the end (image URLs, checksums, versions) are tested together. Change
  them only if you know why.

`build.conf` is ignored by git. It holds passwords, so do not share it.

## Build

```
./build.sh rpi4        # Raspberry Pi 4 image
./build.sh vm-amd64    # VM image for amd64
./build.sh vm-arm64    # VM image for arm64
./build.sh all         # all three, one after the other
./build.sh cert        # new server certificate only (see "Renew the certificate")
./build.sh test        # shellcheck, bats and Go tests in the builder container
```

Options:

- `--rebuild-base`: build the base layer again, even if it is in the cache.
- `--base-only`: build only the base layer (for development and tests).
- `--config FILE`: use another configuration file than `./build.conf`.

Everything runs in a privileged Docker container. Only `out/` and `secrets/` are written on your
computer. The cache lives in the Docker volume `emulationstation-cache`. To start from zero:
`docker volume rm emulationstation-cache`.

- To stop a build, press Ctrl+C (or run `docker stop <id>`). The running step cleans up first
  (mounts, loop devices), then the build ends. Wait for it; this can take a few seconds.
- Only one image build can run at a time, because all builds share the cache. A second build
  stops with a message.
- Old base layers of the same target are removed from the cache when a new one is saved.

### Outputs

In `out/`:

| File | What |
|---|---|
| `emulationstation-rpi4-<date>.img.xz` and `.img.xz.sha256` | Raspberry Pi image |
| `emulationstation-vm-<arch>-<date>.qcow2` | VM disk for QEMU/UTM (16 GiB virtual size, sparse) |
| `emulationstation-vm-<arch>-<date>.raw` | VM disk, raw (for VirtualBox) |
| `emulationstation-ca.crt` | CA certificate for your browser and OS |
| `build-report.txt` | Emulator modules that failed to build, xpadneo state, notes |
| `logs/` | One log per target and step |

Read `build-report.txt` after each build. If an emulator module failed, it is listed there and its
systems are missing in EmulationStation.

`secrets/` holds the CA key. It stays on your computer and is never put into an image. Keep it:
the same CA is used for all later builds, so you import it only once.

## Flash the Raspberry Pi image

Check the file first:

```
cd out && sha256sum -c emulationstation-rpi4-*.img.xz.sha256
```

(macOS: `shasum -a 256 -c ...`.)

With **Raspberry Pi Imager**: choose "Choose OS", then "Use custom", pick the `.img.xz` file, and
choose the SD card. When Imager asks about OS customisation, say no: the image is already set up.

With the command line (Linux):

```
xz -dc out/emulationstation-rpi4-<date>.img.xz | sudo dd of=/dev/sdX bs=4M conv=fsync status=progress
```

**Warning:** `dd` overwrites the device without asking. Check the device name with `lsblk` (macOS:
`diskutil list`, device `/dev/rdiskN`) and make sure it is your SD card and not a system disk.

Put the card into the Pi 4, connect the network and a screen, and power on. On the first boot the
root partition grows to fill the card, SSH host keys are made, and your boot file is read (see below).
The first start takes a little longer.

## Use the VM images

- **QEMU or UTM:** use the `.qcow2` file. It has a virtual size of 16 GiB, so the root file system
  grows to 16 GiB at the first boot. Use UEFI firmware (OVMF for amd64, AAVMF for arm64). Example:

  ```
  qemu-system-x86_64 -machine q35 -accel kvm -cpu host -m 2048 -smp 2 \
    -bios /usr/share/ovmf/OVMF.fd \
    -drive file=emulationstation-vm-amd64-<date>.qcow2,format=qcow2,if=virtio \
    -netdev user,id=n0,hostfwd=tcp::11911-:11911,hostfwd=tcp::11944-:11944 \
    -device virtio-net-pci,netdev=n0 -device virtio-gpu-pci
  ```

- **VirtualBox:** use the `.raw` file. It is not enlarged, so enlarge it before the first boot,
  for example `truncate -s 16G emulationstation-vm-amd64-<date>.raw`, then
  `VBoxManage convertfromraw emulationstation-vm-amd64-<date>.raw vm.vdi --format VDI`. Enable EFI
  in the VM settings. (You can also resize the VDI with `VBoxManage modifymedium disk vm.vdi --resize 16384`.)
- The serial console is active. There is usually no Bluetooth adapter in a VM, and the web UI says so.

## Import the CA certificate

The web UI uses HTTPS with a certificate that is signed by your own CA. Without the CA in your
system, the browser shows a warning, and login over HTTPS is not trusted. Import
`out/emulationstation-ca.crt` once. It stays valid for 10 years and works for every image you build
with the same `secrets/` folder.

- **macOS:** double-click the file, add it to the "login" or "System" keychain, open it, expand
  "Trust" and set "When using this certificate" to "Always Trust". Or:
  `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain out/emulationstation-ca.crt`
- **Windows:** double-click the file, "Install Certificate", store location "Local Machine", place
  it in "Trusted Root Certification Authorities". Or run `certmgr.msc` and import it there.
- **Linux (Debian/Ubuntu):** `sudo cp out/emulationstation-ca.crt /usr/local/share/ca-certificates/emulationstation-ca.crt && sudo update-ca-certificates`.
  Fedora: copy to `/etc/pki/ca-trust/source/anchors/` and run `sudo update-ca-trust`.
- **Firefox** (all systems) has its own store: Settings, Privacy & Security, Certificates, "View
  Certificates", "Authorities", "Import", and tick "Trust this CA to identify websites".
  Chrome and Edge use the system store.
- **Android:** Settings, Security, Encryption & credentials, "Install a certificate", "CA certificate".
  Chrome on Android uses it. Some apps ignore user certificates.
- **iOS/iPadOS:** open the file (send it to the device), allow the profile in Settings, install it, then
  Settings, General, About, Certificate Trust Settings, and switch on full trust for "EmuStation Builder Local CA".

## First boot and `emulationstation.txt`

To set Wi-Fi, host name and passwords without a keyboard, put a file `emulationstation.txt` on the
boot partition. The Pi card shows this partition (`bootfs`) on every computer. For a VM it is the
EFI partition.

1. Find `emulationstation.txt.example` on the partition. Copy it to `emulationstation.txt`.
2. Remove the `#` in front of the lines you want and change the values. The comments in the file
   explain every key.
3. Put the card back and boot.

Keys: `HOSTNAME`, `WIFI_SSID`, `WIFI_PASSWORD`, `WIFI_COUNTRY`, `TIMEZONE`, `KEYMAP`,
`USER_PASSWORD`, `WEB_USER`, `WEB_PASSWORD`. Unknown keys are ignored.

- The file is read at **every** boot, not only the first one. So you can change the Wi-Fi later.
- On the Raspberry Pi, set `WIFI_COUNTRY` together with the Wi-Fi lines. Raspberry Pi OS blocks
  Wi-Fi without a country. If it is missing, the country from `build.conf` is used.
- After it was used, the file is renamed to `emulationstation.txt.applied`. In that copy all
  passwords are `***`. Lines that could not be used are listed at the end with the reason.
- Until the boot, passwords are readable in the file by anyone who holds the card.
- If you change `HOSTNAME`, the certificate stays the same. The new name is valid only if it was in
  `HOSTNAME` or `TLS_EXTRA_SANS` when you built the image. Otherwise the browser shows a
  certificate warning (the boot log says so). Add the name to `TLS_EXTRA_SANS` and build again, or
  renew the certificate.

## Ports and security

- Only two TCP ports are open from outside: **11911** (SSH) and **11944** (web UI, HTTPS only).
  Ping (ICMP and ICMPv6) is allowed. Everything else is dropped.
- Root login over SSH is off. `sudo` asks for the password. Only `reboot`, `poweroff` and the
  helper of the web UI run without it.
- **5 failed logins** (SSH or web UI) from one address ban that address for **1 hour** on that port (SSH failures block SSH, web failures block the web UI).
  You can unban an address in the web UI (System page).
- Basic Auth is only accepted over HTTPS.
- All devices made from one image share the same web server key. Do not expose the device to the
  internet. For a shared image, build your own.

Connect with SSH: `ssh -p 11911 emulationstation@<device>`.

## Change the default access

- **Web UI login:** open the Settings page in the web UI and set a new user and password. On the
  device you can also run `emulationstation-web passwd --config /etc/emulationstation-web/web.conf`
  (it asks for the user and the password twice; run it as the user `emulationstation`). Or set
  `WEB_USER` and `WEB_PASSWORD` in `emulationstation.txt`.
- **Linux password (SSH, console):** log in over SSH and run `passwd`. Or set `USER_PASSWORD` in
  `emulationstation.txt`.

## Renew the certificate

The server certificate is valid for 825 days. To make a new one without a new image:

```
./build.sh cert
```

It uses the existing CA, so you do not import anything again. The command prints the `scp` and
`ssh` commands that copy `server.crt` and `server.key` to the device and restart the web service.
Replace `<device>` in them with the IP address or name of the device.

## Controllers

- `controllers/templates/` holds ready mappings for Xbox 360, Xbox One and Xbox Series pads, and
  DualShock 4 and DualSense pads (USB and Bluetooth). 8BitDo pads in X-Input mode use the Xbox 360
  mapping. They work in EmulationStation and RetroArch without the setup wizard. See the README in
  that folder for details.
- `controllers/custom/` is for your own mappings. Export a mapping on the Controllers page of the
  web UI, put the file here and build again. A custom file wins over a template with the same GUID.
- An unknown controller gets the EmulationStation wizard "Configure Input" at start.
- Bluetooth pads: pair them on the Controllers page of the web UI (scan, then pair). The Pi 4
  has Bluetooth on board. Sony pads use the kernel driver. Xbox pads over Bluetooth use the
  `xpadneo` driver, which is built for the Pi kernel (DKMS).
- **xpadneo caveat:** if the build of `xpadneo` fails, the build report says so. Xbox pads then work
  over USB only. Over Bluetooth, ES uses the USB mapping by name: the buttons are wrong, and the
  wizard does not appear. The "reset" button in the web UI does not help in this case. Connect a USB
  keyboard and use the ES menu "Configure Input" for the pad, or fix `xpadneo` and build again.

## CPU speed and heat (Pi 4)

The Pi 4 slows down by itself when it gets too hot (from about 80 °C). The dashboard shows if
this happens now, and also if the power supply is too weak (read with `vcgencmd get_throttled`
through the root helper).

To keep the Pi cooler, open the System page, tab "CPU", and choose a top CPU speed: Max, 1200,
1000, 800 or 600 MHz. It works at once, and it stays after a reboot (saved in
`/var/lib/emulationstation-cpu/max-mhz`, applied at boot by `emulationstation-cpu.service`).
Lower speeds make heavy games slower. A heat sink or a fan helps more than a lower speed.
The web UI never changes `config.txt` and never sets a speed above the board's own top speed.
The VM images do not show the tab.

## Custom web UI

You can change the look and the texts of the web UI with your own files. Every file you do not
give comes from the web service itself.

- Put the files into `webui-custom/` in the repository, with the same paths as in
  `webui/internal/web/`: `templates/…` (only `.html`) and `static/…`. Example:
  `webui-custom/templates/dashboard.html` replaces the dashboard, and `webui-custom/static/logo.png`
  is served as `/static/logo.png`. Only `templates/` and `static/` are allowed, and symlinks stop
  the build. See `webui-custom/README.md`.
- The build copies the folder to `/home/emulationstation/webui-custom/` on the device. You can
  change the files there over SSH. A new image replaces this folder with the files from the
  repository.
- Templates are loaded when the service starts. After a change, open the System page, tab
  "Web UI", and click "Reload web UI". Static files are read on every request, but browsers keep
  them for one day: reload the page without the cache.
- If your templates have an error, the web UI uses the built-in templates, and the System page
  shows the error.
- Tailwind classes in your templates get CSS only when the files are in `webui-custom/` at build
  time. On the device, use the classes that already exist or your own CSS file.
- Your templates get the same data as the built-in ones. A later update can change this data, so
  check your templates after an update.
- Custom templates from before the redesign ("Noir Grid") must be adapted to the new markup and
  CSS classes. `navItem.Icon` now holds a Lucide icon name, so a copied custom layout must use
  `{{icon .Icon}}`. The `icon` function knows only the icons of the built-in sprite
  (`webui/assets/icons.txt`). Add more with `webui/scripts/icons.sh`. The layout blocks
  `subtitle` and `actions` are optional.
- `UI_DIR` in `/etc/emulationstation-web/web.conf` (default
  `/home/emulationstation/webui-custom`) is the folder. An empty value turns custom files off.

## About & licenses

The page "About & licenses" (`/about`) is reachable from every page. It shows the copyright, the
note that there is no warranty, a link to the source code (from `SOURCE_URL`), the text of the
AGPL and the licenses of the embedded third-party parts.

## Import from a USB stick

You can copy games and BIOS files from a USB stick to the device. Nothing happens by itself. You
start the import in the web UI, on the page "USB".

Supported file systems: FAT32, exFAT and NTFS. If the build report has a line
`usb ntfs3 missing for kernel ...`, NTFS sticks do not work on that image. Use FAT32 or exFAT.

An NTFS stick that was not ejected safely on Windows (or a Windows PC with "Fast Startup") is
"dirty", and the device cannot mount it. The job then says "The USB stick could not be mounted.
... An NTFS stick that was not ejected safely on Windows cannot be mounted." To fix it, plug the
stick into the Windows PC and use "Safely Remove Hardware" (eject) before you pull it out. If
that does not help, run `chkdsk /f X:` on Windows (`X:` is the drive letter of the stick).

Stick layout:

```
emulationstation/
  README.txt          written by the device (how to use the stick)
  roms/<system>/...   for example roms/snes/Zelda.zip or roms/psx/Game/Game.cue
  bios/...            BIOS files
  import-report.txt   written by the device after each import
```

- `<system>` is a system name like in `~/RetroPie/roms/<system>` (for example `snes`, `psx`).
  Sub folders below `roms/<system>/` are allowed. They stay as they are on the device
  (`roms/psx/Game/Game.cue` becomes `~/RetroPie/roms/psx/Game/Game.cue`). Games in sub folders
  work in the web UI like other games: the page "Games" lists them, and you can rename, delete,
  start and scrape them. Deleting such a game also deletes its disc files, and the sub folder when
  it is empty then.
- Scripts (files that end in `.sh`, for example for the system `ports`) are never "ready". They
  always need a decision on the review page, with the note "This file is a script. It runs as a
  program when started."
- BIOS files go directly into `bios/`. The only sub folder that works is `bios/dc/` (Dreamcast).
  Other sub folders are skipped.
- A stick made for the RetroPie USB service (`retropie/roms/`, `retropie/BIOS/`) is read too. Its
  BIOS folder is `retropie/BIOS`. If the stick has both folders, `emulationstation/` is used and
  `retropie/` is ignored.
- Links are never followed. If `emulationstation` is a link or a file, or if only `retropie` is a
  link or a file, or if `roms` or `bios` is one, the import stops with the message "The stick has
  a link or file named X. Remove it on your PC." A `retropie` link next to a real
  `emulationstation/` folder is only ignored. A folder named `README.txt` or `import-report.txt`
  gives the message "The stick has a folder named X. Remove it on your PC."

How to use it:

1. **Prepare.** Plug in a new stick and click "Import" on the page "USB". The device sees that
   the stick has neither an `emulationstation/` nor a `retropie/` folder and creates
   `emulationstation/`, with a folder for every system and
   a `README.txt`. Then it stops.
2. **Fill the stick on your PC.** Copy ROMs into `emulationstation/roms/<system>/` and BIOS files
   into `emulationstation/bios/`.
3. **Import.** Plug the stick in again and click "Import". The device copies the files to the SD
   card. When the job is done, the job page shows a button "Review files". Click it, check the
   systems and names, then click "Apply", like after an upload.
   Each file gets a status in `import-report.txt` on the stick: `staged`, `needs review`,
   `skipped: <reason>` (for example `skipped: damaged archive` for a ZIP that cannot be read, or
   `skipped: archive is too large when unpacked` for a ZIP above `MAX_EXTRACT_MB`),
   `bios saved`, `bios overwritten` or `bios skipped: already imported`.
4. Remove the stick. Every job (also "Prepare" and a job with an error) unmounts the stick at the
   end; the job log then says "You can remove the USB stick now." You only need "Eject" when a
   stick is still mounted, for example when the job log says "Could not unmount the USB stick".
   The page "USB" shows "Eject" only then. After a restart nothing is mounted.

Re-import: you can import the same stick again after you add more files. Files that are already
on the device (same place, same size) are found before copying and skipped. This works for ZIPs
that the device unpacks too: when every file in the ZIP is already on the device (same place,
same size), the ZIP is not copied (`skipped: already imported`). Only new files are copied, and
the free space check counts only them. A ZIP that is skipped (too large when unpacked, too many
files, damaged) counts 0 in the free space check and does not stop the import. A BIOS file with the same name and size is
skipped. A BIOS file with another size is overwritten, because the stick is the newer source.

Nothing is deleted from the stick. The device writes only `emulationstation/README.txt`,
`import-report.txt` and empty folders. The stick is mounted without `exec`, `suid` and `dev`.

Limit: `USB_MAX_FILES` in `/etc/emulationstation-web/web.conf` (default `20000`) is the largest
number of files in one import. Above it, the import stops before it copies anything.

Only use games you own; an image or stick with ROMs is for you only.

## Tests

- `./build.sh test` runs shellcheck, the license header check, the bats tests and the Go tests
  (`go vet`, `go test -race`) in the builder container.
- Static checks of a finished image (read only, in the builder container):
  ```
  test/rpi4-static.sh --stage app out/emulationstation-rpi4-<date>.img.xz
  test/vm-static.sh --stage app out/emulationstation-vm-amd64-<date>.qcow2
  ```
  `--stage base` checks only the base layer. The scripts print PASS or FAIL per check.
- VM smoke test: boots the VM image headless in QEMU and checks the web UI, SSH, the firewall, the
  bans and the boot file. QEMU runs on **your** computer, so install it first
  (Debian/Ubuntu: `sudo apt install qemu-system-x86 qemu-system-arm ovmf qemu-efi-aarch64`;
  macOS: `brew install qemu`). It uses KVM on Linux and HVF on macOS when the CPU architecture of the
  image is the same as yours. Otherwise it uses slow software emulation. Build with the smoke
  configuration, then run the test:
  ```
  ./build.sh vm-amd64 --config test/fixtures/build.conf.smoke
  test/vm-smoke.sh out/emulationstation-vm-amd64-<date>.qcow2
  ```
  The test uses a copy of the image and deletes it at the end. See the header of
  `test/vm-smoke.sh` for details.
- Things that only real hardware can show are in [docs/hardware-checklist.md](docs/hardware-checklist.md).
  Please go through that list with a Pi 4 after each larger change.

## What is verified, and what is not

The scripts and the unit tests are checked automatically. These parts were never run on real
hardware by the author of the code and are listed in the hardware checklist: the Pi boot, the game
start from the web UI on a real terminal, Bluetooth and controller templates on real pads, Wi-Fi
setup through NetworkManager, emulator speed and temperature (N64 and PSP depend on the game), and
the VM display. The most important open points:

- The images use Debian 12 ("bookworm"). It gets LTS security updates until 2028-06-30.
- On the Pi most modules come as RetroPie binaries. A module without a binary is built from
  source and listed in `out/build-report.txt` ("built from source, no binary"). A module that
  fails is skipped and listed there too.
- SDL 2.26 and newer writes a CRC field into controller GUIDs. The templates were made for this,
  and the name of the controller is used as a fallback.


## License

EmuStation Builder is free software under the **GNU Affero General Public License, version 3 or
later** (AGPL-3.0-or-later). See the file `LICENSE`. If you run a changed version as a network
service, you must offer the source of your version to its users. The web UI shows a link to the
source in its footer; set `SOURCE_URL` in `build.conf` for your fork.

Third-party parts keep their own licenses. The image contains RetroPie, many emulators and the
EpicNoir theme. **EpicNoir is licensed under CC BY-NC-SA 4.0 (non-commercial).** So you must not
distribute the images commercially. Build your images for your own use.

The third-party parts that are embedded in the web UI (fonts, icons, scripts) and their licenses
are listed in `webui/internal/legal/THIRD-PARTY-NOTICES` and on the web UI page "About & licenses"
(`/about`).

Source code: <https://github.com/mkloubert/es-builder>

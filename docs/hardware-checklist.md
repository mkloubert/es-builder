# Hardware checklist

License: AGPL-3.0-or-later, see LICENSE

Use this list after a build, with a real Raspberry Pi 4 (2 GB) and, where it says so, with the
VM image. The automatic tests cannot check these things. Each item says what to do and what you
should see. Tick the box when it works. If it does not, write down what you saw (journal, build
report) and open an issue.

Before you start:

- Flash the image (see README.md) and have the first boot file ready if a step needs it.
- Keep `out/build-report.txt` at hand. It lists emulator modules and the xpadneo driver that
  failed to build.
- Log in over SSH (`ssh -p 11911 emulationstation@<device>`) for the commands below.

## 1. Build and image

- [ ] `./build.sh test` is green, and nothing is skipped in the builder container.
  Expected: shellcheck, bats (no skips) and Go tests pass.
- [ ] `./build.sh rpi4`, `./build.sh vm-amd64` (and `vm-arm64` on an arm64 host) finish.
  Expected: files in `out/` as listed in the README. Note the build time per target and the
  failed modules in `out/build-report.txt`.
- [ ] `(cd out && sha256sum -c emulationstation-rpi4-*.img.xz.sha256)` says OK.
- [ ] Your `USER_PASSWORD` is not in the images:
  `xz -dc out/emulationstation-rpi4-*.img.xz | grep -a -c -F '<your password>'` prints `0`.
  Same for `grep -a -c -F '<your password>' out/emulationstation-vm-amd64-*.raw`.
- [ ] `test/rpi4-static.sh --stage app out/emulationstation-rpi4-*.img.xz` and
  `test/vm-static.sh --stage app out/emulationstation-vm-*.qcow2` print only PASS.
- [ ] `test/vm-smoke.sh out/emulationstation-vm-amd64-*.qcow2` (image built with
  `--config test/fixtures/build.conf.smoke`) prints "All checks passed".
- [ ] Second build of the same target is fast (base layer comes from the cache; log says
  "from cache").
- [ ] On Linux, the files in `out/` and `secrets/` belong to your user, and the keys in
  `secrets/` have mode 0600.

## 2. First boot (Pi 4)

- [ ] With `ES_AUTOSTART=yes`: the Pi boots straight into EmulationStation with the EpicNoir
  theme. No wizard, no login prompt, no manual step. Expected: the ES start screen on the TV.
- [ ] With `ES_AUTOSTART=no` (default): tty1 shows a shell (user `emulationstation`) and the hint
  "EmulationStation autostart is off". The web UI dashboard shows "EmulationStation is off" with
  no ES buttons. Upload and controller changes work at once. Typing
  `exec /usr/local/lib/emulationstation/es-session` starts ES; the web UI then shows "running",
  and the ES buttons work.
- [ ] The root file system grows to the size of the card. `df -h /` shows about the card size.
- [ ] `cat /etc/machine-id` shows 32 hex digits (not `uninitialized`). SSH host keys exist in
  `/etc/ssh/ssh_host_*`.
- [ ] `systemd-analyze verify emulationstation-firstboot.service` prints nothing, and
  `systemctl list-dependencies --after emulationstation-firstboot.service` shows no cycle.
- [ ] `systemctl is-enabled systemd-firstboot` says `masked`, and the console shows no first
  boot question (locale, keyboard, time zone).
- [ ] Raspberry Pi OS wizard does not appear (userconfig is masked).
- [ ] Overscan is off: the picture fills the screen without black borders (`disable_overscan=1`).
- [ ] `swapon --show` lists zram (`/dev/zram0`) and no swap file.
- [ ] Boot with the ES start from a cold card takes an acceptable time (note it).

## 3. Boot file `emulationstation.txt`

Make the file on the boot partition (`bootfs`) from `emulationstation.txt.example`. Use a Windows
editor, so the file has CRLF line ends. Set host name, Wi-Fi, time zone, keyboard, `USER_PASSWORD`,
`WEB_USER` and `WEB_PASSWORD`. Boot.

- [ ] `journalctl -u emulationstation-firstboot` shows each setting as used. A wrong line is
  listed with the reason.
- [ ] `cat /boot/firmware/emulationstation.txt.applied` (VM: `/boot/efi/`): every password is
  `***`. The original file is gone.
- [ ] `hostnamectl` shows the new host name.
- [ ] Wi-Fi: `nmcli connection show emulationstation-wifi` exists and the Pi joins the network.
  Test an SSID with spaces, with `\` and with `;`, and an SSID that is only digits. NetworkManager
  must accept the key file for each of them.
- [ ] `raspi-config nonint do_wifi_country DE` works at this point of the boot on bookworm
  (see the journal of the first boot service). The Wi-Fi country is set.
- [ ] SSH login with the new password works. Web login with the new web user and password works.
- [ ] `stat -c '%U %a' /etc/emulationstation-web/web.conf` prints `emulationstation 600`.
- [ ] Reboot with the file still there (edit a value, for example the Wi-Fi): the file is read at
  every boot and the change is applied.
- [ ] A host name that is not in the certificate gives a certificate warning in the browser, and
  the boot log says so.

## 4. Network, SSH and firewall

- [ ] From another computer: `nmap -p 1-65535 <device>` shows only 11911 and 11944 open.
  Ping works (IPv4 and IPv6).
- [ ] On the device: `ss -tlnp` shows only 11911 and 11944 on non-loopback addresses.
- [ ] `ssh -p 11911 emulationstation@<device>` works with the password. `ssh -p 11911 root@<device>`
  is rejected. Port 22 is closed.
- [ ] `sudo` asks for the password. `sudo reboot` and `sudo poweroff` work without it.
- [ ] Five wrong SSH passwords from one computer: the next connection is refused for 1 hour
  (`sudo fail2ban-client status sshd` lists the address). The failed logins come from `sshd`
  (OpenSSH 9.2 on bookworm). Unban in the web UI (System page).
- [ ] Five wrong web logins: the web UI stops answering for that address. Unban in the web UI
  from another address, or on the device with
  `sudo /usr/local/lib/emulationstation/priv f2b-unban <ip>`.
- [ ] `sudo systemctl restart nftables`, then `sudo nft list table inet f2b-table`: the table and
  the ban are still there.
- [ ] No avahi, cups, rpcbind or exim listens (`ss -tulpn`).
- [ ] The VM also: `ss -l --vsock` shows no listener (no vsock on port 22).

## 5. Web UI

Open `https://<device>:11944` after you imported the CA (see README.md). The browser must show no
warning.

- [ ] Login with `zocken`/`zocken` (or your values) works. The footer shows the source link.
- [ ] `systemctl status emulationstation-web` is active. Pages: dashboard, games, upload,
  controllers, BIOS, system, settings.
- [ ] Upload a 700 MB PSX `.chd` file over Wi-Fi. Expected: the upload finishes without an error
  and without a time-out. Note the time. After "apply" and an ES restart, the game shows up with a
  clean name.
- [ ] Upload a ZIP with ROMs. Expected: the ROMs are extracted to the right system folder.
- [ ] Change the web password on the settings page, log out and in with the new password.
- [ ] Dashboard: restart ES, stop ES, start ES. State and buttons follow.
- [ ] System page: apt update, apt upgrade, reboot and power off work (the helper runs with
  sudo and the service has `NoNewPrivileges=no`). The journal of
  `emulationstation-web`, `ssh` and `fail2ban` is shown.
- [ ] System page shows the ban list and the unban button works.
- [ ] Scrape a game with ScreenScraper (needs an account in the settings).
- [ ] Custom web UI: over SSH, copy `templates/dashboard.html` from the repo to
  `~/webui-custom/templates/` and change a text. System page, tab "Web UI", "Reload web UI".
  Expected: the toast "Web UI reloaded.", the card shows "1 custom files in use", the dashboard
  shows the new text. Then break the file (for example remove `{{end}}`) and reload. Expected: the
  error card, and the built-in dashboard still works.
- [ ] Redesign on a phone: the menu button opens and closes the menu. With a keyboard, Escape
  closes the menu.
- [ ] Redesign on a phone: the "..." menus open and close, and their entries can be tapped.
- [ ] Redesign on a phone: the upload dropzone accepts a file (tap to choose, or drop on a
  tablet).
- [ ] Redesign: the fonts render as Bebas Neue (headings), Inter (text) and Geist Mono (labels, buttons), and
  not as a fallback font. No request goes to the internet (browser network tab).
- [ ] Page "About & licenses" opens from the footer of every page and shows the copyright, the
  warranty note, the source link and the license texts.

## 6. EmulationStation and games

- [ ] SSH login does not start ES a second time (the autostart runs on tty1 only).
- [ ] ES menu: "Restart EmulationStation", "Restart System" and "Shutdown System" work.
- [ ] Start one game per system group and quit it. ES comes back each time. Systems to try: NES,
  SNES, Game Boy, GBA, Mega Drive, PC Engine, arcade, PlayStation, Dreamcast, a computer system
  (C64 or Amiga), DOS, ScummVM. Expected: sound and picture, normal speed.
- [ ] C64 music: upload a `.sid` file in the web UI. Expected: the review page offers
  "Commodore 64 Music", the file lands in `~/RetroPie/roms/c64music/`, and the games page of this
  system has no "Scrape" button. Start it in ES. Expected: VICE's `vsid` plays the tune, and you can
  leave the player (F12 menu or the RetroPie hotkey) and get back to ES.
- [ ] N64 and PSP: they depend on the game. Try one game of each. A slow game is not a bug (README).
- [ ] Start a game from the web UI. Expected: the game runs on the TV and "stop game" in the web UI
  ends it and ES returns. (The game process group and `runcommand` on a real terminal were only
  tested with a simulated terminal.)
- [ ] Signals on tty1 (keyboard on the Pi): press Ctrl+C, Ctrl+\ and Ctrl+Z while ES runs.
  Expected: ES and the session keep running (the wrapper catches these keys).
- [ ] Press Ctrl+C in a running game. Expected: the game ends and ES starts again. Press Ctrl+Z in a
  running game and in the runcommand menu (shown when a game starts). Expected: the wrapper
  continues the stopped game after a moment, the keyboard still works in the menu, and nothing
  hangs. This was proven only with a simulated terminal.
- [ ] Temperature under load: play N64 for 15 minutes, then run `vcgencmd measure_temp`.
  Expected: below 80 C. Also check `vcgencmd get_throttled` prints `throttled=0x0`.
- [ ] Modules listed as failed in `out/build-report.txt`: the systems of these modules are missing in ES.
  This is expected. Write down which ones.

## 6a. CPU speed and heat (Pi 4)

- [ ] `vcgencmd get_throttled` (as root, for example on the console) prints `throttled=0x0`.
  Expected: the dashboard shows "Not throttled." under "CPU temperature". If the line is
  missing, check the journal of `emulationstation-web` for `cpu-throttled` and note it.
- [ ] System page, tab "CPU": choose 800 MHz, "Save". Expected: the toast "CPU speed limit
  saved.", the text "Top CPU speed: 800 MHz", and
  `cat /sys/devices/system/cpu/cpufreq/policy0/scaling_max_freq` prints `800000`.
- [ ] Reboot. Expected: still 800000 and "Top CPU speed: 800 MHz"
  (`systemctl status emulationstation-cpu` ran without an error).
- [ ] With a weak power supply (or a long thin USB cable), play a heavy game. Expected: the
  dashboard shows the power line ("Power is too low …").
- [ ] Choose "Max" again. Expected: `scaling_max_freq` is the board top speed (1500000 or
  1800000).

## 7. Controllers

The templates are in `controllers/templates/` (see the README there). A template controller must
work in ES **and** in RetroArch without the "Configure Input" wizard. Test each controller you own,
once with the menu in ES and once in a game.

- [ ] ES on bookworm writes the SDL names "Xbox 360 Controller", "Xbox One S Controller" and
  "Xbox Series X Controller" as `deviceName`, and GUIDs with a CRC. Check with
  `grep -A3 inputConfig ~/.emulationstation/es_input.cfg` after you ran the wizard on a test pad.
- [ ] Xbox 360 pad over USB (version 0x0114): works without the wizard. Other revisions and 8BitDo
  in X-Input mode may report another version. Then the name match must still work.
- [ ] Xbox One S and Xbox Series pad over USB (version 0x0517, can depend on the firmware).
- [ ] Xbox One S and Xbox Series pad over Bluetooth with xpadneo: name "Xbox Wireless Controller",
  IDs 045e:028e, version 0x1130. A Series pad in BLE mode must connect through BlueZ.
- [ ] Xbox pad over Bluetooth **without xpadneo** (only if `out/build-report.txt` has an `xpadneo`
  line, which means DKMS failed). Expected: ES uses the USB mapping by name, so the buttons are
  wrong, and no wizard appears. The "reset" button in the web UI does not help here. Fix: connect a
  USB keyboard (ES has a default keyboard mapping) and use the ES menu "Configure Input" for the
  pad. Or fix xpadneo and build again. Or log in over SSH, remove the `<inputConfig>` node with
  that `deviceName` or GUID from `~/.emulationstation/es_input.cfg` and restart ES.
- [ ] DualShock 4 over USB: name "Sony Computer Entertainment Wireless Controller" (v1) or
  "Sony Interactive Entertainment Wireless Controller" (v2).
- [ ] DualShock 4 over Bluetooth: version 0x8100 (some pads 0x8001) and name "Wireless Controller".
  Expected: works. A third-party pad with the same generic name may get the DS4 mapping by mistake.
- [ ] DualSense over USB (name "Sony Interactive Entertainment DualSense Wireless Controller") and
  over Bluetooth (name "DualSense Wireless Controller", version 0x8100).
- [ ] An unknown controller (not in the templates) gets the ES wizard "Configure Input" at start.
- [ ] Bluetooth service runs on the Pi 4: `systemctl status bluetooth`, `bluetoothctl list` shows an
  adapter. There is no `disable-bt` overlay.
- [ ] Pair a pad in the web UI (Controllers page, scan, pair). It reconnects after a reboot.
  On the VM the page says "no Bluetooth adapter found".
- [ ] Controller export: on the Controllers page export a mapping you made with the wizard, put it
  into `controllers/custom/`, build again. Expected: the pad works without the wizard. Known
  limits: exports of nodes written by RetroPie may miss hotkey, trigger and thumb buttons in
  RetroArch. The web UI may show "not configured" for Xbox pads because it compares kernel names
  and ES writes SDL names.
- [ ] Import a mapping on the Controllers page and see that the pad works.
- [ ] `hid-playstation` handles the PlayStation pads (`dmesg | grep -i playstation`).

## 8. VM image (QEMU/UTM/VirtualBox)

- [ ] `qemu-img info out/emulationstation-vm-amd64-*.qcow2` shows a virtual size of 16 GiB.
- [ ] Boot the qcow2 without a resize: `df -h /` shows about 16G after the first boot, and a second
  boot does not call `growpart` again.
- [ ] Raw image: enlarge the disk first (`truncate -s 16G file.raw` or
  `VBoxManage modifymedium disk file.vdi --resize 16384`), then boot. The root grows to it.
- [ ] No first boot question on the console. The serial console works.
- [ ] GRUB: `grep root= /boot/grub/grub.cfg` shows no `/dev/loop`. The build log says whether
  `update-grub` or the fallback was used.
- [ ] EmulationStation shows on the virtio-gpu display (software rendering; the KMS build on a VM
  is untested). Games are not a goal of the VM.
- [ ] Exactly one kernel is installed (`ls /lib/modules`).
- [ ] The VM has the systems "Commodore 64", "Commodore 64 Music", "Dreamcast" and "PlayStation"
  (when `vice`, `lr-flycast` and `lr-pcsx-rearmed` are not in the build report). The web UI
  accepts a `.d64`, a `.sid`, a `.cdi` and a `.cue`. `mc` and `ifconfig` work in an SSH session.
  Starting a game is optional (software rendering, Dreamcast is slow).

## 9. Builder on the host

- [ ] `./build.sh` on macOS with Docker Desktop, OrbStack or Colima: the Docker check, the binfmt
  check for a foreign architecture and the disk space check work.
- [ ] `./build.sh cert` writes `out/cert/server.crt` and `server.key`. The printed `scp` and `ssh`
  commands install them on a running device, and the web UI works with the new certificate after
  the service restart.
- [ ] A second `./build.sh <target>` after a changed `build.conf` value (for example `WEB_PASSWORD`)
  does not rebuild the base layer.

## 10. USB import

See the design in `docs/superpowers/specs/2026-10-28-usb-import-design.md`. You need three sticks
(or one stick, formatted three times): FAT32, exFAT and NTFS.

- [ ] After a build, `test/rpi4-static.sh --stage app` and `test/vm-static.sh --stage app` show no
  FAIL for "USB" (a WARN for `ntfs3` means NTFS sticks do not work on that kernel).
- [ ] FAT32 stick: plug it in, open "USB" in the web UI, click "Refresh". The stick shows with its
  label and size. The first "Import" prepares the stick. Put a ROM and a BIOS file on it on a PC,
  import again, commit on the review page. The game shows up in ES.
- [ ] exFAT stick: the same steps work.
- [ ] NTFS stick: the same steps work (the kernel driver `ntfs3`).
- [ ] NTFS stick from Windows that was pulled out without "Safely Remove Hardware" (or with Fast
  Startup on). Expected: a clear error "The USB stick could not be mounted. ... An NTFS stick that
  was not ejected safely on Windows cannot be mounted. Eject it safely and try again." and no
  helper usage text. After a safe eject on Windows (or `chkdsk /f`), the import works.
- [ ] Pull the stick out during an import. Expected: the job says "The USB stick was removed.",
  nothing half-copied stays in the upload list, and the page "USB" works again after you plug in
  a stick.
- [ ] Import the same stick again. Expected: "Nothing new", no questions, no copies.
- [ ] Add one new ROM to that stick and import again. Expected: only the new file is staged.
- [ ] Re-import a stick with many zipped cartridge ROMs (for example `roms/snes/*.zip`) when the
  SD card is nearly full. Expected: the ZIPs that were unpacked before are `skipped: already
  imported`, nothing is copied again, and there is no "Not enough space" error.
- [ ] Import a game in a sub folder (for example `roms/psx/Game/Game.cue` + `Game.bin`). Expected:
  the page "Games" lists it; rename, start, scrape and delete work. Delete removes the `.bin`
  too, and the folder `Game` when it is empty.
- [ ] A `.sh` file (for example in `roms/ports/`) is shown under "Need a decision" with the note
  "This file is a script. It runs as a program when started."
- [ ] Pi that boots from a USB SSD (only if you have one): the root disk is not listed on the
  page "USB" and cannot be mounted.
- [ ] The mount is not visible in an SSH shell: during an import, `findmnt /run/emulationstation-usb`
  in the SSH session shows nothing (the web service has its own mount namespace), and the import
  still works.
- [ ] After "Eject" or the end of a job, the stick can be pulled out without a file system
  warning on the PC.
- [ ] `import-report.txt` on the stick lists each file with its status.

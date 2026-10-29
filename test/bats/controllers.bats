#!/usr/bin/env bats
# EmuStation Builder
# Copyright (C) 2026  Marcel Joachim Kloubert
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <http://www.gnu.org/licenses/>.

# Task 10: controller templates (controllers/templates/*.xml), provision/common/app-50-controllers.sh
# (merge into es_input.cfg, RetroPie hook, Bluetooth) and the controller checks of the static
# image tests. The real chroot run happens in the builder container.
# shellcheck disable=SC2016,SC2030,SC2031,SC2034,SC2154

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  TPL="$ROOT/controllers/templates"
  FIX="$ROOT/test/fixtures/controllers"
  STUBS="$BATS_TEST_TMPDIR/stubs"
  CALLS="$BATS_TEST_TMPDIR/calls"
  mkdir -p "$STUBS"
  : >"$CALLS"
  export CALLS
}

# The device files from the brief (one per device and bus)
TEMPLATES="xbox360-usb xboxone-usb xboxseries-usb xboxone-bt xboxseries-bt ds4-usb ds4-bt dualsense-usb dualsense-bt"

load_app50() {
  # shellcheck source=provision/common/app-50-controllers.sh
  . "$ROOT/provision/common/app-50-controllers.sh"
}

# py <script> <args...> : python3 in isolated mode
py() { python3 -I -c "$@"; }

# Same algorithm as webui/internal/controllers/guid.go (SDL_crc16 = CRC-16/ARC, little endian fields)
GUID_PY='
import re, struct, sys

def crc16(data):
    crc = 0
    for b in data:
        crc ^= b
        for _ in range(8):
            crc = (crc >> 1) ^ 0xA001 if crc & 1 else crc >> 1
    return crc

def guid(bus, vendor, product, version, name, with_crc):
    crc = crc16(name.encode()) if with_crc else 0
    return struct.pack("<HHHHHHHH", bus, crc, vendor, 0, product, 0, version, 0).hex()
'

@test "GUID helper of the tests matches the reference values of the web UI" {
  run py "$GUID_PY"'
assert crc16(b"123456789") == 0xBB3D
assert guid(3, 0x045e, 0x028e, 0x0114, "Microsoft X-Box 360 pad", False) == "030000005e0400008e02000014010000"
print("ok")'
  echo "$output"
  [ "$output" = ok ]
}

@test "templates: one file per device and bus from the brief, plus README.md and custom/.gitkeep" {
  local t
  for t in $TEMPLATES; do
    [ -f "$TPL/$t.xml" ] || { echo "missing: $t.xml"; return 1; }
  done
  [ -f "$TPL/README.md" ]
  [ -f "$ROOT/controllers/custom/.gitkeep" ]
  [ ! -s "$ROOT/controllers/custom/.gitkeep" ]
}

@test "templates: xmllint --noout (well-formed)" {
  command -v xmllint >/dev/null || skip "xmllint is missing (runs in the builder container)"
  run xmllint --noout "$TPL"/*.xml
  echo "$output"
  [ "$status" -eq 0 ]
}

@test "templates: well-formed XML (python3), root inputList, joystick nodes only, no keyboard" {
  run py '
import sys, glob, xml.dom.minidom as M
files = sorted(glob.glob(sys.argv[1] + "/*.xml"))
assert files, "no templates"
for f in files:
    doc = M.parse(f)
    root = doc.documentElement
    assert root.tagName == "inputList", f
    nodes = root.getElementsByTagName("inputConfig")
    assert len(nodes) >= 2, f + ": need the GUID with and without CRC"
    for n in nodes:
        assert n.getAttribute("type") == "joystick", f
        assert n.getAttribute("deviceName") and n.getAttribute("deviceGUID"), f
    assert "keyboard" not in open(f).read().split("-->")[-1], f
print("ok")' "$TPL"
  echo "$output"
  [ "$output" = ok ]
}

# Every "guid-source:" line gives bus/vendor/product/version/kernel name. For each line the file
# must have a node with the GUID with CRC and one without. Every node GUID must come from a line.
@test "templates: every GUID matches its guid-source line, with and without CRC" {
  run py "$GUID_PY"'
import glob, xml.dom.minidom as M
src_re = re.compile(r"guid-source: bus=(0x[0-9a-f]{2}) vendor=(0x[0-9a-f]{4}) product=(0x[0-9a-f]{4}) version=(0x[0-9a-f]{4}) name=\"([^\"]+)\"")
files = sorted(glob.glob(sys.argv[1] + "/*.xml"))
assert len(files) >= 9, "templates are missing"
for f in files:
    text = open(f).read()
    srcs = src_re.findall(text)
    assert srcs, f + ": no guid-source line"
    got = {n.getAttribute("deviceGUID") for n in M.parse(f).getElementsByTagName("inputConfig")}
    want = set()
    for bus, v, p, ver, name in srcs:
        args = [int(x, 16) for x in (bus, v, p, ver)]
        want.add(guid(*args, name, True))
        want.add(guid(*args, name, False))
    assert got == want, "%s: got %s want %s" % (f, sorted(got), sorted(want))
print("ok")' "$TPL"
  echo "$output"
  [ "$output" = ok ]
}

@test "templates: GUIDs match 'emulationstation-web guid' (Plan 1 CLI)" {
  command -v go >/dev/null || skip "go is missing (runs in the builder container)"
  local bin="$BATS_TEST_TMPDIR/esw" f line bus v p ver name g
  (cd "$ROOT/webui" && go build -o "$bin" ./cmd/emulationstation-web)
  for f in "$TPL"/*.xml; do
    while IFS= read -r line; do
      bus=$(sed -n 's/.*bus=\(0x[0-9a-f]*\).*/\1/p' <<<"$line")
      v=$(sed -n 's/.*vendor=\(0x[0-9a-f]*\).*/\1/p' <<<"$line")
      p=$(sed -n 's/.*product=\(0x[0-9a-f]*\).*/\1/p' <<<"$line")
      ver=$(sed -n 's/.*version=\(0x[0-9a-f]*\).*/\1/p' <<<"$line")
      name=$(sed -n 's/.*name="\([^"]*\)".*/\1/p' <<<"$line")
      for g in "$("$bin" guid --bus "$bus" --vendor "$v" --product "$p" --version "$ver" --name "$name" --crc)" \
        "$("$bin" guid --bus "$bus" --vendor "$v" --product "$p" --version "$ver" --name "$name")"; do
        grep -q "deviceGUID=\"$g\"" "$f" || { echo "$f: $g missing"; return 1; }
      done
    done < <(grep 'guid-source:' "$f")
  done
}

# Button positions from the brief: ES a = east, b = south, x = north, y = west;
# hotkeyenable = select; d-pad hat 0 (1/2/4/8); sticks 0/1 and 3/4; triggers axes 2/5.
@test "templates: all 25 ES inputs with the button codes of the brief" {
  run py '
import sys, glob, os, xml.dom.minidom as M
xbox = dict(b=0, a=1, y=2, x=3, leftshoulder=4, rightshoulder=5, select=6, start=7, hotkeyenable=6, leftthumb=9, rightthumb=10)
ps = dict(b=0, a=1, x=2, y=3, leftshoulder=4, rightshoulder=5, select=8, start=9, hotkeyenable=8, leftthumb=11, rightthumb=12)
common = {
    "up": ("hat", 0, 1), "right": ("hat", 0, 2), "down": ("hat", 0, 4), "left": ("hat", 0, 8),
    "lefttrigger": ("axis", 2, 1), "righttrigger": ("axis", 5, 1),
    "leftanalogup": ("axis", 1, -1), "leftanalogdown": ("axis", 1, 1),
    "leftanalogleft": ("axis", 0, -1), "leftanalogright": ("axis", 0, 1),
    "rightanalogup": ("axis", 4, -1), "rightanalogdown": ("axis", 4, 1),
    "rightanalogleft": ("axis", 3, -1), "rightanalogright": ("axis", 3, 1),
}
files = sorted(glob.glob(sys.argv[1] + "/*.xml"))
assert len(files) >= 9, "templates are missing"
for f in files:
    buttons = xbox if os.path.basename(f).startswith("xbox") else ps
    want = dict(common)
    want.update({k: ("button", v, 1) for k, v in buttons.items()})
    for n in M.parse(f).getElementsByTagName("inputConfig"):
        got = {}
        for i in n.getElementsByTagName("input"):
            got[i.getAttribute("name")] = (i.getAttribute("type"), int(i.getAttribute("id")), int(i.getAttribute("value")))
        assert len(got) == 25 and got == want, "%s %s: %s" % (f, n.getAttribute("deviceGUID"), sorted(set(got.items()) ^ set(want.items())))
print("ok")' "$TPL"
  echo "$output"
  [ "$output" = ok ]
}

@test "templates: vendorId/productId (decimal, as ES writes them) match the guid-source line" {
  run py '
import sys, re, glob, xml.dom.minidom as M
files = sorted(glob.glob(sys.argv[1] + "/*.xml"))
assert len(files) >= 9, "templates are missing"
for f in files:
    pairs = {(int(v, 16), int(p, 16)) for v, p in re.findall(r"guid-source: bus=0x.. vendor=(0x[0-9a-f]{4}) product=(0x[0-9a-f]{4})", open(f).read())}
    for n in M.parse(f).getElementsByTagName("inputConfig"):
        assert (int(n.getAttribute("vendorId")), int(n.getAttribute("productId"))) in pairs, f
print("ok")' "$TPL"
  echo "$output"
  [ "$output" = ok ]
}

@test "templates: comments cite sources and mark unverified values" {
  local f
  for f in "$TPL"/*.xml; do
    grep -q 'Sources:' "$f" || { echo "$f: no Sources"; return 1; }
  done
  grep -q 'UNVERIFIED' "$TPL/xboxone-usb.xml"
  grep -q 'xpadneo' "$TPL/xboxseries-bt.xml"
}

# --- app-50-controllers.sh: es_xml collect --------------------------------------

@test "es_xml collect: templates first, custom wins per GUID and runs last; keyboard nodes are skipped" {
  load_app50
  local out="$BATS_TEST_TMPDIR/nodes"
  mkdir -p "$out"
  run es_xml collect "$out" "$FIX/tpl-a.xml" "$FIX/tpl-b.xml" "$FIX/custom-export.xml" "$FIX/custom-keyboard.xml"
  echo "$output"
  [ "$status" -eq 0 ]
  # stdout: <node file>\t<device name>, in run order
  [ "$(printf '%s\n' "$output" | grep -v '^skip' | cut -f2)" = "$(printf '%s\n' 'Pad A' 'Pad B' 'Pad A custom' 'Other pad')" ]
  [[ "$output" == *"skip"*"keyboard"* ]]
  local last
  last=$(printf '%s\n' "$output" | grep 'Pad A custom' | cut -f1)
  grep -q 'deviceGUID="03000000aaaa00000100000001000000"' "$out/$last"
  grep -q '<input name="a" type="button" id="7" value="1"/>' "$out/$last"
  # each node file is a complete es_temporaryinput.cfg
  run py 'import sys, xml.dom.minidom as M
for f in sys.argv[1:]:
    r = M.parse(f).documentElement
    assert r.tagName == "inputList" and len(r.getElementsByTagName("inputConfig")) == 1
print("ok")' "$out"/*.cfg
  [ "$output" = ok ]
  run grep -l keyboard "$out"/*.cfg
  [ "$status" -ne 0 ]
}

@test "es_xml collect: broken XML or a node without GUID -> error" {
  load_app50
  printf '<inputList><inputConfig type="joystick" deviceName="x">\n' >"$BATS_TEST_TMPDIR/bad.xml"
  run es_xml collect "$BATS_TEST_TMPDIR" "$BATS_TEST_TMPDIR/bad.xml"
  [ "$status" -ne 0 ]
  printf '<inputList><inputConfig type="joystick" deviceName="x"/></inputList>\n' >"$BATS_TEST_TMPDIR/bad.xml"
  run es_xml collect "$BATS_TEST_TMPDIR" "$BATS_TEST_TMPDIR/bad.xml"
  [ "$status" -ne 0 ]
  [[ "$output" == *deviceGUID* ]]
}

# --- app-50-controllers.sh: es_xml merge ----------------------------------------

@test "es_xml merge: replaces the node with the same GUID, keeps other nodes, inputAction and the keyboard" {
  load_app50
  local cfg="$BATS_TEST_TMPDIR/es_input.cfg" out="$BATS_TEST_TMPDIR/nodes"
  mkdir -p "$out"
  cp "$FIX/es_input.cfg" "$cfg"
  es_xml collect "$out" "$FIX/custom-export.xml" >"$BATS_TEST_TMPDIR/list"
  es_xml merge "$cfg" "$out/$(cut -f1 "$BATS_TEST_TMPDIR/list")"
  cat "$cfg"
  run py 'import sys, xml.dom.minidom as M
r = M.parse(sys.argv[1]).documentElement
assert r.tagName == "inputList"
cfgs = r.getElementsByTagName("inputConfig")
guids = [c.getAttribute("deviceGUID").lower() for c in cfgs]
assert guids.count("03000000aaaa00000100000001000000") == 1, guids
assert "03000000bbbb00000200000001000000" in guids
assert "-1" in guids
n = [c for c in cfgs if c.getAttribute("deviceGUID").lower() == "03000000aaaa00000100000001000000"][0]
assert n.getAttribute("deviceName") == "Pad A custom"
assert len(r.getElementsByTagName("inputAction")) == 1
print("ok")' "$cfg"
  echo "$output"
  [ "$output" = ok ]
  head -n1 "$cfg" | grep -qx '<?xml version="1.0"?>'
}

@test "es_xml merge: a missing es_input.cfg is created; a second merge of the same node changes nothing" {
  load_app50
  local cfg="$BATS_TEST_TMPDIR/new/es_input.cfg" out="$BATS_TEST_TMPDIR/nodes"
  mkdir -p "$out" "$BATS_TEST_TMPDIR/new"
  es_xml collect "$out" "$FIX/tpl-a.xml" >"$BATS_TEST_TMPDIR/list"
  while IFS=$'\t' read -r node _; do es_xml merge "$cfg" "$out/$node"; done <"$BATS_TEST_TMPDIR/list"
  cp "$cfg" "$BATS_TEST_TMPDIR/first"
  while IFS=$'\t' read -r node _; do es_xml merge "$cfg" "$out/$node"; done <"$BATS_TEST_TMPDIR/list"
  cmp "$cfg" "$BATS_TEST_TMPDIR/first"
  [ "$(grep -c '<inputConfig' "$cfg")" -eq 2 ]
  run grep -q 'type="keyboard"' "$cfg"
  [ "$status" -ne 0 ]
}

@test "joy_file: same file name rule as RetroPie (retroarch.sh onend)" {
  load_app50
  [ "$(joy_file 'Xbox 360 Controller')" = 'Xbox 360 Controller.cfg' ]
  [ "$(joy_file 'Microsoft Xbox Series S|X Controller')" = 'Microsoft Xbox Series SX Controller.cfg' ]
  [ "$(joy_file 'a:b<c>d?e"f/g\h*i')" = 'abcdefghi.cfg' ]
}

# --- app-50-controllers.sh: full run with stubs ----------------------------------

# Fake RetroPie root: es_input.cfg as RetroPie creates it, retroarch-joypads, the hook path.
fake_root() {
  local r=$1
  mkdir -p "$r/home/emulationstation/.emulationstation" "$r/opt/retropie/configs/all/retroarch-joypads" \
    "$r/opt/retropie/supplementary/emulationstation/scripts" "$r/home/emulationstation"
  printf '<?xml version="1.0"?>\n<inputList>\n  <inputAction type="onfinish">\n    <command>/opt/retropie/supplementary/emulationstation/scripts/inputconfiguration.sh</command>\n  </inputAction>\n</inputList>\n' \
    >"$r/home/emulationstation/.emulationstation/es_input.cfg"
  : >"$r/opt/retropie/supplementary/emulationstation/scripts/inputconfiguration.sh"
  printf 'root:x:0:0::/root:/bin/bash\nemulationstation:x:1000:1000::/home/emulationstation:/bin/bash\n' >"$r/etc_passwd"
}

# runuser stub that acts like the RetroPie hook: logs the call and the device name of
# es_temporaryinput.cfg and writes retroarch-joypads/<name>.cfg (and moves an old one to .bak).
make_hook_stub() {
  cat >"$STUBS/runuser" <<'EOF'
#!/usr/bin/env bash
echo "runuser $*" >>"$CALLS"
tmp="$PROVISION_ROOT/home/emulationstation/.emulationstation/es_temporaryinput.cfg"
[ -f "$tmp" ] || { echo "no es_temporaryinput.cfg" >&2; exit 1; }
name=$(sed -n 's/.*deviceName="\([^"]*\)".*/\1/p' "$tmp")
echo "hook $name" >>"$CALLS"
# HOOK_FAIL=<device name>: the hook writes nothing for this device
[ "${HOOK_FAIL:-}" = "$name" ] && exit 0
dir="$PROVISION_ROOT/opt/retropie/configs/all/retroarch-joypads"
f="$dir/${name//[:><?\"\/\\|*]/}.cfg"
[ -f "$f" ] && mv "$f" "$f.bak"
printf 'input_device = "%s"\n' "$name" >"$f"
EOF
  printf '#!/usr/bin/env bash\necho "chown $*" >>"$CALLS"\n' >"$STUBS/chown"
  printf '#!/usr/bin/env bash\necho "systemctl $*" >>"$CALLS"\n' >"$STUBS/systemctl"
  printf '#!/usr/bin/env bash\n[ "$1" = passwd ] && grep "^$2:" "$PROVISION_ROOT/etc_passwd"\n' >"$STUBS/getent"
  chmod +x "$STUBS"/*
}

# provision directory with the controllers of the repo (or of a fixture)
fake_prov() {
  local p=$1 src=$2 custom=${3:-}
  mkdir -p "$p/controllers/templates" "$p/controllers/custom"
  cp "$src"/*.xml "$p/controllers/templates/"
  if [ -n "$custom" ]; then cp "$custom" "$p/controllers/custom/"; fi
}

@test "app-50-controllers.sh: real templates -> all GUIDs in es_input.cfg, one joypad file per name, bluetooth enabled" {
  make_hook_stub
  local r="$BATS_TEST_TMPDIR/r" p="$BATS_TEST_TMPDIR/prov"
  fake_root "$r"
  fake_prov "$p" "$TPL"
  PATH="$STUBS:$PATH" run env PROVISION_ROOT="$r" PROVISION_DIR="$p" bash "$ROOT/provision/common/app-50-controllers.sh"
  echo "$output"
  [ "$status" -eq 0 ]
  local cfg="$r/home/emulationstation/.emulationstation/es_input.cfg" g n
  while IFS= read -r g; do
    [ "$(grep -c "deviceGUID=\"$g\"" "$cfg")" -eq 1 ] || { echo "GUID $g"; return 1; }
  done < <(sed -n 's/.*deviceGUID="\([^"]*\)".*/\1/p' "$TPL"/*.xml | sort -u)
  grep -q '<inputAction type="onfinish">' "$cfg"
  run grep -q 'type="keyboard"' "$cfg"
  [ "$status" -ne 0 ]
  while IFS= read -r n; do
    [ -f "$r/opt/retropie/configs/all/retroarch-joypads/$n.cfg" ] || { echo "joypad $n"; return 1; }
  done < <(sed -n 's/.*deviceName="\([^"]*\)".*/\1/p' "$TPL"/*.xml | sort -u)
  # no leftovers: temporary file, .bak files of this run
  [ ! -e "$r/home/emulationstation/.emulationstation/es_temporaryinput.cfg" ]
  run ls "$r"/opt/retropie/configs/all/retroarch-joypads/*.bak
  [ "$status" -ne 0 ]
  grep -qx 'systemctl enable bluetooth.service' "$CALLS"
  grep -q '^runuser -u emulationstation -- env HOME=/home/emulationstation .*bash /opt/retropie/supplementary/emulationstation/scripts/inputconfiguration.sh$' "$CALLS"
  grep -q '^chown emulationstation:emulationstation .*/es_input.cfg$' "$CALLS"
}

@test "app-50-controllers.sh: custom file wins for the same GUID and its hook runs last" {
  make_hook_stub
  local r="$BATS_TEST_TMPDIR/r" p="$BATS_TEST_TMPDIR/prov"
  fake_root "$r"
  mkdir -p "$BATS_TEST_TMPDIR/tpl"
  cp "$FIX/tpl-a.xml" "$FIX/tpl-b.xml" "$BATS_TEST_TMPDIR/tpl/"
  fake_prov "$p" "$BATS_TEST_TMPDIR/tpl" "$FIX/custom-export.xml"
  PATH="$STUBS:$PATH" run env PROVISION_ROOT="$r" PROVISION_DIR="$p" bash "$ROOT/provision/common/app-50-controllers.sh"
  echo "$output"
  [ "$status" -eq 0 ]
  [ "$(grep '^hook ' "$CALLS" | tail -n1)" = "hook Pad A custom" ]
  local cfg="$r/home/emulationstation/.emulationstation/es_input.cfg"
  grep -q 'deviceName="Pad A custom" deviceGUID="03000000aaaa00000100000001000000"' "$cfg"
  [ "$(grep -c 'deviceGUID="03000000aaaa00000100000001000000"' "$cfg")" -eq 1 ]
}

@test "app-50-controllers.sh: hook writes no joypad file -> abort (also when an old file exists)" {
  make_hook_stub
  local r="$BATS_TEST_TMPDIR/r" p="$BATS_TEST_TMPDIR/prov"
  fake_root "$r"
  fake_prov "$p" "$TPL"
  # an old file with the name of the first node must not hide the failure
  : >"$r/opt/retropie/configs/all/retroarch-joypads/Wireless Controller.cfg"
  PATH="$STUBS:$PATH" HOOK_FAIL="Wireless Controller" run env PROVISION_ROOT="$r" PROVISION_DIR="$p" bash "$ROOT/provision/common/app-50-controllers.sh"
  echo "$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"did not write retroarch-joypads/Wireless Controller.cfg"* ]]
  [ ! -e "$r/home/emulationstation/.emulationstation/es_temporaryinput.cfg" ]
}

@test "app-50-controllers.sh: no templates -> abort" {
  make_hook_stub
  local r="$BATS_TEST_TMPDIR/r" p="$BATS_TEST_TMPDIR/prov"
  fake_root "$r"
  mkdir -p "$p/controllers/templates" "$p/controllers/custom"
  PATH="$STUBS:$PATH" run env PROVISION_ROOT="$r" PROVISION_DIR="$p" bash "$ROOT/provision/common/app-50-controllers.sh"
  [ "$status" -ne 0 ]
  [[ "$output" == *"no controller templates"* ]]
}

@test "40-provision-app.sh stages controllers/ (templates and custom) for the app scripts" {
  # shellcheck source=/dev/null
  . "$ROOT/builder/steps/40-provision-app.sh"
  local root="$BATS_TEST_TMPDIR/root"
  mkdir -p "$root"
  CFG_USER_PASSWORD=secret
  app_stage_provision "$root" rpi4
  [ -f "$root/root/provision/controllers/templates/xbox360-usb.xml" ]
  [ -d "$root/root/provision/controllers/custom" ]
}

# --- static checks (test/rpi4-static.sh, test/vm-static.sh) ---------------------

# Root with the controller results of the real templates (other app checks may FAIL here)
controller_root() {
  local r=$1 n g
  mkdir -p "$r/opt/retropie/configs/all/emulationstation" "$r/opt/retropie/configs/all/retroarch/autoconfig" \
    "$r/etc/systemd/system/bluetooth.target.wants" "$r/home/emulationstation"
  # RetroPie makes ~/.emulationstation and retroarch-joypads absolute symlinks
  ln -s /opt/retropie/configs/all/emulationstation "$r/home/emulationstation/.emulationstation"
  ln -s /opt/retropie/configs/all/retroarch/autoconfig "$r/opt/retropie/configs/all/retroarch-joypads"
  ln -s /usr/lib/systemd/system/bluetooth.service "$r/etc/systemd/system/bluetooth.target.wants/bluetooth.service"
  {
    echo '<?xml version="1.0"?>'
    echo '<inputList>'
    sed -n 's/.*deviceGUID="\([^"]*\)".*/\1/p' "$TPL"/*.xml | sort -u | while IFS= read -r g; do
      printf '  <inputConfig type="joystick" deviceName="x" deviceGUID="%s"/>\n' "$g"
    done
    echo '</inputList>'
  } >"$r/opt/retropie/configs/all/emulationstation/es_input.cfg"
  while IFS= read -r n; do
    : >"$r/opt/retropie/configs/all/retroarch/autoconfig/$n.cfg"
  done < <(sed -n 's/.*deviceName="\([^"]*\)".*/\1/p' "$TPL"/*.xml | sort -u)
}

@test "rpi4-static.sh and vm-static.sh --stage app: controller and Bluetooth checks PASS on a good root" {
  local r="$BATS_TEST_TMPDIR/r" s
  controller_root "$r"
  for s in rpi4-static.sh vm-static.sh; do
    ARCH_ARG=()
    [ "$s" = rpi4-static.sh ] || ARCH_ARG=(--arch amd64)
    run bash "$ROOT/test/$s" --stage app "${ARCH_ARG[@]}" --root "$r"
    echo "$output" | grep -E 'controller|es_input|retroarch|bluetooth|keyboard' || true
    [[ "$output" == *"PASS: es_input.cfg has all template GUIDs"* ]]
    [[ "$output" == *"PASS: es_input.cfg has no keyboard node"* ]]
    [[ "$output" == *"PASS: retroarch-joypads has one file per template device name"* ]]
    [[ "$output" == *"PASS: bluetooth.service enabled"* ]]
  done
}

@test "rpi4-static.sh and vm-static.sh --stage app: missing GUID, keyboard node, missing joypad file, bluetooth off -> FAIL" {
  local r="$BATS_TEST_TMPDIR/r" s cfg
  controller_root "$r"
  cfg="$r/opt/retropie/configs/all/emulationstation/es_input.cfg"
  sed -i '3d' "$cfg"
  sed -i 's#</inputList>#  <inputConfig type="keyboard" deviceName="Keyboard" deviceGUID="-1"/>\n</inputList>#' "$cfg"
  rm -f "$r/opt/retropie/configs/all/retroarch/autoconfig/Xbox 360 Controller.cfg"
  rm -f "$r/etc/systemd/system/bluetooth.target.wants/bluetooth.service"
  for s in rpi4-static.sh vm-static.sh; do
    ARCH_ARG=()
    [ "$s" = rpi4-static.sh ] || ARCH_ARG=(--arch amd64)
    run bash "$ROOT/test/$s" --stage app "${ARCH_ARG[@]}" --root "$r"
    [ "$status" -ne 0 ]
    [[ "$output" == *"FAIL: es_input.cfg has all template GUIDs"* ]]
    [[ "$output" == *"FAIL: es_input.cfg has no keyboard node"* ]]
    [[ "$output" == *"FAIL: retroarch-joypads has one file per template device name"* ]]
    [[ "$output" == *"FAIL: bluetooth.service enabled"* ]]
  done
}

@test "check-license-headers skips user exports in controllers/custom" {
  mkdir -p "$BATS_TEST_TMPDIR/r/scripts" "$BATS_TEST_TMPDIR/r/controllers/custom"
  cp "$ROOT/scripts/check-license-headers.sh" "$BATS_TEST_TMPDIR/r/scripts/"
  cp "$FIX/custom-export.xml" "$BATS_TEST_TMPDIR/r/controllers/custom/pad.xml"
  run "$BATS_TEST_TMPDIR/r/scripts/check-license-headers.sh"
  echo "$output"
  [ "$status" -eq 0 ]
}

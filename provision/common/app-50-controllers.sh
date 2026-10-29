#!/usr/bin/env bash
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

# App layer, in the chroot: controller presets (spec 5.5) and Bluetooth.
#  - Reads $PROVISION_DIR/controllers/templates/*.xml and then controllers/custom/*.xml (each
#    sorted). For the same GUID the later node wins and runs last, so custom/ wins over templates/.
#    Keyboard nodes (and anything else that is not type="joystick") are skipped.
#  - For each node: writes es_temporaryinput.cfg (an <inputList> with only this node), runs
#    RetroPie's inputconfiguration.sh as user emulationstation, checks that it wrote
#    retroarch-joypads/<deviceName>.cfg, removes es_temporaryinput.cfg and puts the node into
#    es_input.cfg (a node with the same GUID is replaced, all other nodes stay).
#  - Enables bluetooth.service.
# Checked at RetroPie-Setup f5ebfba6: inputconfiguration.sh takes the home directory from the
# user that runs it ("id -un"), reads ~/.emulationstation/es_temporaryinput.cfg with xmlstarlet
# and runs configscripts/*.sh. They write files only (no display needed): retroarch.sh writes
# /opt/retropie/configs/all/retroarch-joypads/<name>.cfg (an older file with the same device name
# becomes .bak), emulationstation.sh adds a short node with the GUID to es_input.cfg (replaced
# by the full node here). ~/.emulationstation (read by ES and the hook) is a symlink to
# /opt/retropie/configs/all/emulationstation and retroarch-joypads one to retroarch/autoconfig
# (both made by RetroPie).
# The XML work is done with python3 (stdlib, in the image through python3-systemd) so it can be
# tested without the image. .bak files made by this run are removed.
# Environment: PROVISION_DIR. PROVISION_ROOT (tests only): path prefix. When sourced, only the
# functions load.
set -euo pipefail

log() { printf '[app-50-controllers] %s\n' "$*" >&2; }
die() {
  printf '[app-50-controllers] ERROR: %s\n' "$*" >&2
  exit 1
}

U=emulationstation
R=${PROVISION_ROOT:-}
ES_DIR=/home/$U/.emulationstation
JOY_DIR=/opt/retropie/configs/all/retroarch-joypads
HOOK=/opt/retropie/supplementary/emulationstation/scripts/inputconfiguration.sh

# Python part of es_xml. Commands:
#   collect <outdir> <file>... : reads the files in order (root <inputList> or a single
#     <inputConfig>, as the web UI exports it). Later nodes replace earlier ones with the same GUID
#     (case does not matter) and move to the end. Writes each result node as <outdir>/NNN.cfg
#     (a complete es_temporaryinput.cfg) and prints "NNN.cfg<TAB>deviceName" per node in run order.
#   merge <es_input.cfg> <node.cfg> : removes every inputConfig with the GUID of the node and
#     appends the node. Other nodes, inputAction and comments stay. A missing file is created.
ES_XML_PY='
import os, sys, tempfile, xml.dom.minidom as M

def fail(msg):
    sys.stderr.write("[app-50-controllers] ERROR: " + msg + "\n")
    sys.exit(1)

def parse(path):
    try:
        return M.parse(path)
    except Exception as e:
        fail("%s: not valid XML: %s" % (path, e))

def strip_space(node):
    for c in list(node.childNodes):
        if c.nodeType == c.TEXT_NODE and not c.data.strip():
            node.removeChild(c)
        elif c.nodeType == c.ELEMENT_NODE:
            strip_space(c)

def pretty(node, indent):
    lines = node.toprettyxml(indent="  ").rstrip("\n").split("\n")
    return "".join(indent + l + "\n" for l in lines)

def input_list(children):
    return "<?xml version=\"1.0\"?>\n<inputList>\n" + "".join(pretty(c, "  ") for c in children) + "</inputList>\n"

def nodes_of(path):
    root = parse(path).documentElement
    if root.tagName == "inputConfig":
        found = [root]
    elif root.tagName == "inputList":
        found = [c for c in root.childNodes if c.nodeType == c.ELEMENT_NODE and c.tagName == "inputConfig"]
    else:
        fail("%s: root element is %s, want inputList or inputConfig" % (path, root.tagName))
    out = []
    for n in found:
        typ = n.getAttribute("type")
        if typ != "joystick":
            sys.stderr.write("skip: %s: node type \"%s\" is not a joystick\n" % (path, typ))
            continue
        if not n.getAttribute("deviceName") or not n.getAttribute("deviceGUID"):
            fail("%s: joystick node needs deviceName and deviceGUID" % path)
        strip_space(n)
        out.append(n)
    return out

def collect(outdir, files):
    nodes = []
    for f in files:
        for n in nodes_of(f):
            g = n.getAttribute("deviceGUID").lower()
            nodes = [m for m in nodes if m.getAttribute("deviceGUID").lower() != g]
            nodes.append(n)
    for i, n in enumerate(nodes):
        name = "%03d.cfg" % i
        with open(os.path.join(outdir, name), "w") as f:
            f.write(input_list([n]))
        print("%s\t%s" % (name, n.getAttribute("deviceName")))

def merge(cfg, nodefile):
    node = nodes_of(nodefile)[0]
    g = node.getAttribute("deviceGUID").lower()
    kept = []
    if os.path.exists(cfg):
        root = parse(cfg).documentElement
        if root.tagName != "inputList":
            fail("%s: root element is %s, want inputList" % (cfg, root.tagName))
        strip_space(root)
        for c in root.childNodes:
            if c.nodeType == c.ELEMENT_NODE and c.tagName == "inputConfig" and c.getAttribute("deviceGUID").lower() == g:
                continue
            kept.append(c)
    mode = os.stat(cfg).st_mode & 0o7777 if os.path.exists(cfg) else 0o644
    fd, tmp = tempfile.mkstemp(dir=os.path.dirname(os.path.abspath(cfg)), prefix=".es_input.")
    with os.fdopen(fd, "w") as f:
        f.write(input_list(kept + [node]))
    os.chmod(tmp, mode)
    os.replace(tmp, cfg)

cmd = sys.argv[1] if len(sys.argv) > 1 else ""
if cmd == "collect" and len(sys.argv) >= 3:
    collect(sys.argv[2], sys.argv[3:])
elif cmd == "merge" and len(sys.argv) == 4:
    merge(sys.argv[2], sys.argv[3])
else:
    fail("usage: es_xml collect <outdir> <file>... | merge <es_input.cfg> <node.cfg>")
'

# es_xml <command> <args...> : see ES_XML_PY
es_xml() { python3 -I -c "$ES_XML_PY" "$@"; }

# joy_file <deviceName> : file name of the RetroArch autoconfig, same rule as RetroPie
# (configscripts/retroarch.sh, onend_retroarch_joystick)
joy_file() {
  local name=$1
  printf '%s.cfg\n' "${name//[:><?\"\/\\|*]/}"
}

# run_hook : inputconfiguration.sh as user emulationstation (as ES runs it after the wizard)
run_hook() {
  runuser -u "$U" -- env HOME="/home/$U" USER="$U" LOGNAME="$U" bash "$HOOK"
}

# list_bak <dir> : the .bak files in <dir>, one per line (follows a symlinked <dir>)
list_bak() {
  find "$1/" -maxdepth 1 -name '*.bak' -print 2>/dev/null || true
}

apply_controllers() {
  local ctl=${PROVISION_DIR:?PROVISION_DIR is missing}/controllers
  local es="$R$ES_DIR" joy="$R$JOY_DIR" work f node name rc
  local files=() tpl=()
  shopt -s nullglob
  tpl=("$ctl"/templates/*.xml)
  files=("${tpl[@]}" "$ctl"/custom/*.xml)
  shopt -u nullglob
  [ "${#tpl[@]}" -gt 0 ] || die "no controller templates in $ctl/templates"
  [ -f "$R$HOOK" ] || die "RetroPie hook is missing: $HOOK"
  [ -d "$es" ] || die "ES config directory is missing: $ES_DIR"
  [ -d "$joy" ] || die "RetroArch joypad directory is missing: $JOY_DIR"

  work=$(mktemp -d)
  # shellcheck disable=SC2064  # expand now: $work is local
  trap "rm -rf '$work' '$es/es_temporaryinput.cfg'" EXIT
  es_xml collect "$work" "${files[@]}" >"$work/list"
  { list_bak "$es"; list_bak "$joy"; } | LC_ALL=C sort >"$work/bak-before"

  while IFS=$'\t' read -r node name; do
    log "Controller: $name ($node)"
    cp "$work/$node" "$es/es_temporaryinput.cfg"
    chown "$U:$U" "$es/es_temporaryinput.cfg"
    chmod 0644 "$es/es_temporaryinput.cfg"
    # remove the file of an earlier node with the same name, so the check below sees this run
    # (the hook would only move it to .bak, and .bak files of this run are removed at the end)
    rm -f "$joy/$(joy_file "$name")"
    rc=0
    run_hook </dev/null >&2 || rc=$?
    rm -f "$es/es_temporaryinput.cfg"
    [ "$rc" -eq 0 ] || log "WARNING: inputconfiguration.sh exit code $rc for $name"
    [ -f "$joy/$(joy_file "$name")" ] ||
      die "inputconfiguration.sh did not write retroarch-joypads/$(joy_file "$name")"
    es_xml merge "$es/es_input.cfg" "$work/$node"
    # the hook of the next node edits es_input.cfg as user emulationstation
    chown "$U:$U" "$es/es_input.cfg"
    chmod 0644 "$es/es_input.cfg"
  done <"$work/list"

  # .bak files of this run (the hook moves older files of the same device name)
  { list_bak "$es"; list_bak "$joy"; } | LC_ALL=C sort >"$work/bak-after"
  while IFS= read -r f; do
    rm -f "$f"
  done < <(LC_ALL=C comm -13 "$work/bak-before" "$work/bak-after")
  log "$(wc -l <"$work/list") controller nodes in es_input.cfg"
  rm -rf "$work"
  trap - EXIT
}

main() {
  getent passwd "$U" >/dev/null || die "User $U is missing"
  apply_controllers
  systemctl enable bluetooth.service
  log "Bluetooth enabled"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi

# shellcheck shell=sh
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

# Login shells: add the sbin folders to the END of PATH, so tools like ifconfig work without the
# full path (spec 2026-10-31-c64-music-vm-systems-design.md 3.2b). Only folders that are missing
# are added. At the end nothing can hide a program of the same name in an earlier folder, and
# no rights change: programs that need root still need sudo.
for _es_dir in /usr/local/sbin /usr/sbin /sbin; do
  case ":$PATH:" in
    *":$_es_dir:"*) ;;
    *) PATH="$PATH:$_es_dir" ;;
  esac
done
unset _es_dir
export PATH

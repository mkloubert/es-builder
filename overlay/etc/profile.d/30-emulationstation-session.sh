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

# Login shells on tty1 of the user emulationstation: "emulationstation" without arguments starts
# the session wrapper es-session, so the web UI can control ES (spec
# 2026-10-31-es-session-detect-design.md 4.1). With arguments, and in every other shell (SSH,
# other tty), the normal command runs. ES_SESSION and ES_RUN_DIR are for tests only.
case "$-" in
  *i*)
    if [ "$(id -un 2>/dev/null)" = emulationstation ] && [ "$(tty 2>/dev/null)" = /dev/tty1 ]; then
      emulationstation() {
        _es_pid=
        if [ "$#" -gt 0 ]; then
          command emulationstation "$@"
          return
        fi
        _es_pid=$(cat "${ES_RUN_DIR:-/run/emulationstation}/session.pid" 2>/dev/null)
        case "$_es_pid" in
          '' | *[!0-9]*) ;;
          *)
            if [ "$_es_pid" -gt 1 ] && kill -0 "$_es_pid" 2>/dev/null; then
              echo "EmulationStation runs already in its session (PID $_es_pid)."
              unset _es_pid
              return 1
            fi
            ;;
        esac
        unset _es_pid
        exec "${ES_SESSION:-/usr/local/lib/emulationstation/es-session}"
      }
    fi
    ;;
esac

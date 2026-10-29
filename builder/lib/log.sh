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

# Shared log functions (host: Bash 3.2, container: Bash 5).
# Output is in English; all log lines go to stderr.
set -euo pipefail

if [ -t 2 ]; then
  _LOG_RED=$'\033[31m'
  _LOG_YELLOW=$'\033[33m'
  _LOG_BLUE=$'\033[34m'
  _LOG_OFF=$'\033[0m'
else
  _LOG_RED=""
  _LOG_YELLOW=""
  _LOG_BLUE=""
  _LOG_OFF=""
fi

log_info() { printf '%s[INFO]%s %s\n' "$_LOG_BLUE" "$_LOG_OFF" "$*" >&2; }
log_warn() { printf '%s[WARNING]%s %s\n' "$_LOG_YELLOW" "$_LOG_OFF" "$*" >&2; }
log_error() { printf '%s[ERROR]%s %s\n' "$_LOG_RED" "$_LOG_OFF" "$*" >&2; }

# die <message> [exitcode]
die() {
  local code=1
  if [ $# -ge 2 ]; then code=$2; fi
  log_error "$1"
  exit "$code"
}

# log_file <target> <step> -> path out/logs/<target>-<step>.log (the directory is created).
# LOG_DIR overrides the directory (default: out/logs relative to the working directory).
log_file() {
  local dir=${LOG_DIR:-out/logs}
  mkdir -p "$dir"
  printf '%s/%s-%s.log' "$dir" "$1" "$2"
}

# run_logged <target> <step> <command...>
# Runs the command and writes stdout+stderr to the log file and the console.
# The exit code is the one of the command (pipefail).
# tee ignores INT/TERM: on Ctrl+C the command can still write its cleanup messages (and does
# not die from SIGPIPE in the middle of its cleanup).
run_logged() {
  local target=$1 step=$2 file
  shift 2
  file=$(log_file "$target" "$step")
  log_info "Step '$step' ($target) -> $file"
  "$@" 2>&1 | (
    trap '' INT TERM
    exec tee "$file"
  )
}

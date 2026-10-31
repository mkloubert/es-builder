// EmuStation Builder
// Copyright (C) 2026  Marcel Joachim Kloubert
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package es

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// clockTicks is USER_HZ, the unit of the start time in /proc/<pid>/stat
// (100 on every Linux architecture this project builds for).
const clockTicks = 100

// Proc is one process as seen in /proc.
type Proc struct {
	PID, PPID, UID int
	Start          time.Time // zero when unknown
	Args           []string
	Exe            string // target of /proc/<pid>/exe; "" when it cannot be read
}

// Procs lists the processes of the system (spec 2026-10-31-es-session-detect-design.md 4.2).
type Procs interface {
	List() ([]Proc, error)
}

// ProcFS reads the processes from a proc file system (Root, default "/proc").
// A process that ends while it is read is skipped.
type ProcFS struct{ Root string }

// List returns all processes that could be read.
func (p ProcFS) List() ([]Proc, error) {
	root := p.Root
	if root == "" {
		root = "/proc"
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	boot := bootTime(root)
	var out []Proc
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if pr, ok := readProc(filepath.Join(root, e.Name()), pid, boot); ok {
			out = append(out, pr)
		}
	}
	return out, nil
}

// readProc reads one /proc/<pid> folder. ok is false when the status file is gone.
func readProc(dir string, pid int, boot time.Time) (Proc, bool) {
	pr := Proc{PID: pid, PPID: -1, UID: -1}
	st, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return pr, false
	}
	sc := bufio.NewScanner(bytes.NewReader(st))
	for sc.Scan() {
		k, v, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		switch k {
		case "PPid":
			pr.PPID, _ = strconv.Atoi(f[0])
		case "Uid":
			pr.UID, _ = strconv.Atoi(f[0])
		}
	}
	if pr.PPID < 0 || pr.UID < 0 {
		return pr, false
	}
	if b, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		b = bytes.TrimRight(b, "\x00")
		if len(b) > 0 {
			pr.Args = strings.Split(string(b), "\x00")
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil && !boot.IsZero() {
		if ticks, ok := startTicks(string(b)); ok {
			pr.Start = boot.Add(time.Duration(ticks) * time.Second / clockTicks)
		}
	}
	pr.Exe, _ = os.Readlink(filepath.Join(dir, "exe"))
	return pr, true
}

// startTicks returns field 22 (starttime) of a /proc/<pid>/stat line. The process name (field
// 2) can hold spaces and ")", so the fields are counted from the last ")".
func startTicks(stat string) (int64, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(stat[i+1:])
	// f[0] is field 3 (state), so field 22 is f[19]
	if len(f) < 20 {
		return 0, false
	}
	n, err := strconv.ParseInt(f[19], 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// bootTime reads "btime" from <root>/stat; zero when unknown.
func bootTime(root string) time.Time {
	b, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return time.Time{}
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[0] == "btime" {
			if n, err := strconv.ParseInt(f[1], 10, 64); err == nil && n > 0 {
				return time.Unix(n, 0)
			}
		}
	}
	return time.Time{}
}

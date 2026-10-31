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
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	testRC = "/opt/retropie/supplementary/runcommand/runcommand.sh"
	testES = "/opt/retropie/supplementary/emulationstation/emulationstation"
)

// fakeProcs returns a fixed process list.
type fakeProcs struct {
	list []Proc
	err  error
}

func (f fakeProcs) List() ([]Proc, error) { return f.list, f.err }

// withProcs sets a fake process list and an info file path below the run dir.
func withProcs(c *Controller, list ...Proc) *Controller {
	c.Procs = fakeProcs{list: list}
	c.InfoFile = filepath.Join(c.RunDir, "runcommand.info")
	c.gameGrace = 2 * time.Second
	return c
}

func writeInfo(t *testing.T, c *Controller, content string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(c.InfoFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(c.InfoFile, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// menu procs: ES (100) -> sh -c "<line>" (101) -> runcommand (102) -> emulator (103)
func menuTree(uid int, start time.Time) []Proc {
	return []Proc{
		{PID: 100, PPID: 1, UID: uid, Exe: testES, Args: []string{testES}},
		{PID: 101, PPID: 100, UID: uid, Args: []string{"sh", "-c", testRC + " 0 _SYS_ c64 /roms/c64/FOO.d64"}},
		{PID: 102, PPID: 101, UID: uid, Start: start, Args: []string{"/bin/bash", testRC, "0", "_SYS_", "c64", "/roms/c64/FOO.d64"}},
		{PID: 103, PPID: 102, UID: uid, Args: []string{"/opt/retropie/emulators/vice/bin/x64"}},
	}
}

func TestStateMenuGame(t *testing.T) {
	uid := os.Getuid()
	start := time.Now().Add(-time.Minute)
	info := "c64\nvice-x64\n/roms/c64/FOO.d64\n/opt/x.sh x64 FOO\n"
	cases := []struct {
		name       string
		session    bool
		state      string
		procs      []Proc
		infoTime   time.Time
		noInfo     bool
		wantKind   string
		wantSystem string
		wantROM    string
		wantMenu   bool
	}{
		{name: "menu game in the session", session: true, state: "es\n", procs: menuTree(uid, start),
			infoTime: start.Add(time.Second), wantKind: "game", wantSystem: "c64", wantROM: "/roms/c64/FOO.d64", wantMenu: true},
		{name: "menu game without a session", procs: menuTree(uid, start),
			infoTime: start, wantKind: "game", wantSystem: "c64", wantROM: "/roms/c64/FOO.d64", wantMenu: true},
		{name: "stale info file gives no names", session: true, state: "es\n", procs: menuTree(uid, start),
			infoTime: start.Add(-time.Hour), wantKind: "game", wantMenu: true},
		{name: "missing info file gives no names", session: true, state: "es\n", procs: menuTree(uid, start),
			noInfo: true, wantKind: "game", wantMenu: true},
		{name: "web UI game wins", session: true, state: "game\tsnes\t/roms/snes/A.sfc\n", procs: menuTree(uid, start),
			infoTime: start, wantKind: "game", wantSystem: "snes", wantROM: "/roms/snes/A.sfc"},
		{name: "runcommand of another user is ignored", session: true, state: "es\n", procs: menuTree(uid+1, start),
			infoTime: start, wantKind: "es"},
		{name: "only the sh -c line is no game", session: true, state: "es\n", procs: menuTree(uid, start)[:2],
			infoTime: start, wantKind: "es"},
		{name: "ES without a session is unmanaged", procs: menuTree(uid, start)[:1], wantKind: "unmanaged"},
		{name: "ES in the session is not unmanaged", session: true, state: "es\n", procs: menuTree(uid, start)[:1], wantKind: "es"},
		{name: "ES of another user is not unmanaged", procs: menuTree(uid+1, start)[:1], wantKind: "off"},
		{name: "nothing runs", wantKind: "off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCtlNoSession(t)
			if tc.session {
				c = newCtl(t)
				write(t, c, "state", tc.state)
			}
			withProcs(c, tc.procs...)
			if !tc.noInfo {
				writeInfo(t, c, info, tc.infoTime)
			}
			got := c.State()
			if got.Kind != tc.wantKind || got.System != tc.wantSystem || got.ROM != tc.wantROM || got.Menu != tc.wantMenu {
				t.Fatalf("State() = %+v, want kind=%s system=%q rom=%q menu=%v", got, tc.wantKind, tc.wantSystem, tc.wantROM, tc.wantMenu)
			}
		})
	}
}

func TestStateIgnoresProcsError(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "es\n")
	c.Procs = fakeProcs{err: errors.New("boom")}
	if got := c.State(); got.Kind != "es" {
		t.Fatalf("State() = %+v", got)
	}
}

func TestMenuGameBlocksESStop(t *testing.T) {
	c := withProcs(newCtl(t), menuTree(os.Getuid(), time.Time{})...)
	write(t, c, "state", "es\n")
	ran := false
	err := c.WithESStopped(context.Background(), func() error { ran = true; return nil })
	if !errors.Is(err, ErrGameRunning) || ran {
		t.Fatalf("WithESStopped = %v, ran = %v", err, ran)
	}
	if err := c.Stop(); !errors.Is(err, ErrGameRunning) {
		t.Fatalf("Stop = %v", err)
	}
	if exists(c, "hold") {
		t.Fatal("hold file left behind")
	}
}

func TestUnmanagedESBlocksActions(t *testing.T) {
	c := withProcs(newCtlNoSession(t), menuTree(os.Getuid(), time.Time{})[:1]...)
	ran := false
	err := c.WithESStopped(context.Background(), func() error { ran = true; return nil })
	if !errors.Is(err, ErrUnmanaged) || ran {
		t.Fatalf("WithESStopped = %v, ran = %v", err, ran)
	}
	if err := c.Stop(); !errors.Is(err, ErrUnmanaged) {
		t.Fatalf("Stop = %v", err)
	}
	if err := c.Launch("snes", "/roms/snes/A.sfc"); !errors.Is(err, ErrUnmanaged) {
		t.Fatalf("Launch = %v", err)
	}
	if exists(c, "hold") || exists(c, "launch") {
		t.Fatal("files left behind")
	}
}

func TestMenuGameWithoutSessionBlocksApply(t *testing.T) {
	c := withProcs(newCtlNoSession(t), menuTree(os.Getuid(), time.Time{})...)
	ran := false
	err := c.WithESStopped(context.Background(), func() error { ran = true; return nil })
	if !errors.Is(err, ErrGameRunning) || ran {
		t.Fatalf("WithESStopped = %v, ran = %v", err, ran)
	}
}

// startTree starts real processes: es (sleep, the ES stand-in) and runcommand (sh that waits
// for its child sleep, the emulator). It returns the PIDs and a fake process list that tells
// the controller es -> runcommand -> emulator.
func startTree(t *testing.T) (esPID, rcPID, emuPID int, list []Proc) {
	t.Helper()
	es := spawn(t)
	rc := exec.Command("sh", "-c", `sleep 30 & echo "$!"; wait "$!"`)
	out, err := rc.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	emu, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = rc.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(emu, syscall.SIGKILL)
		_ = syscall.Kill(rc.Process.Pid, syscall.SIGKILL)
	})
	uid := os.Getuid()
	list = []Proc{
		{PID: es.Process.Pid, PPID: 1, UID: uid, Exe: testES},
		{PID: rc.Process.Pid, PPID: es.Process.Pid, UID: uid, Args: []string{"/bin/bash", testRC, "0", "_SYS_", "c64", "x"}},
		{PID: emu, PPID: rc.Process.Pid, UID: uid, Args: []string{"x64"}},
	}
	return es.Process.Pid, rc.Process.Pid, emu, list
}

func TestStopMenuGameKeepsES(t *testing.T) {
	esPID, rcPID, emuPID, list := startTree(t)
	c := withProcs(newCtl(t), list...)
	write(t, c, "state", "es\n")
	if err := c.StopGame(); err != nil {
		t.Fatalf("StopGame = %v", err)
	}
	if syscall.Kill(emuPID, 0) != syscall.ESRCH {
		t.Error("emulator still runs")
	}
	if !waitGone(rcPID, time.Second) {
		t.Error("runcommand still runs")
	}
	if err := syscall.Kill(esPID, 0); err != nil {
		t.Errorf("ES stand-in was hit: %v", err)
	}
}

func TestStopMenuGameWithoutEmulator(t *testing.T) {
	esPID := spawn(t).Process.Pid
	rc := spawn(t) // runcommand without children (its menu): it gets SIGTERM itself
	uid := os.Getuid()
	c := withProcs(newCtl(t),
		Proc{PID: esPID, PPID: 1, UID: uid, Exe: testES},
		Proc{PID: rc.Process.Pid, PPID: esPID, UID: uid, Args: []string{"/bin/bash", testRC}},
	)
	write(t, c, "state", "es\n")
	if err := c.StopGame(); err != nil {
		t.Fatalf("StopGame = %v", err)
	}
	if !waitGone(rc.Process.Pid, time.Second) {
		t.Error("runcommand still runs")
	}
	if err := syscall.Kill(esPID, 0); err != nil {
		t.Errorf("ES stand-in was hit: %v", err)
	}
}

func TestStopGameWithoutGame(t *testing.T) {
	c := withProcs(newCtl(t))
	write(t, c, "state", "es\n")
	if err := c.StopGame(); !errors.Is(err, ErrNoGame) {
		t.Fatalf("StopGame = %v", err)
	}
}

func TestDescendantsSkipsOtherUsersAndSelf(t *testing.T) {
	uid := os.Getuid()
	self := os.Getpid()
	list := []Proc{
		{PID: 10, PPID: 1, UID: uid},
		{PID: 11, PPID: 10, UID: uid},
		{PID: 12, PPID: 11, UID: uid + 1},
		{PID: 13, PPID: 12, UID: uid},
		{PID: self, PPID: 10, UID: uid},
		{PID: 14, PPID: 99, UID: uid},
	}
	got := descendants(list, 10, uid)
	want := []int{11, 13}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("descendants = %v, want %v", got, want)
	}
}

func TestProcFS(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("stat", "cpu  1 2 3\nbtime 1700000000\nprocesses 5\n")
	mk("42/status", "Name:\tbash\nPPid:\t7\nUid:\t1000\t1000\t1000\t1000\n")
	mk("42/cmdline", "/bin/bash\x00"+testRC+"\x000\x00_SYS_\x00c64\x00/roms/A b.d64\x00")
	// the name has a space and ")": fields are counted from the last ")"
	mk("42/stat", "42 (run cmd) x) S 7 42 42 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 12345 0 0")
	if err := os.Symlink(testES, filepath.Join(root, "42", "exe")); err != nil {
		t.Fatal(err)
	}
	mk("43/cmdline", "x\x00") // no status: the process ended while it was read
	mk("self/status", "PPid:\t1\nUid:\t0\n")
	list, err := ProcFS{Root: root}.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("List = %+v", list)
	}
	p := list[0]
	wantStart := time.Unix(1700000000, 0).Add(12345 * time.Second / clockTicks)
	if p.PID != 42 || p.PPID != 7 || p.UID != 1000 || p.Exe != testES || !p.Start.Equal(wantStart) {
		t.Fatalf("proc = %+v (start want %v)", p, wantStart)
	}
	if len(p.Args) != 6 || p.Args[1] != testRC || p.Args[5] != "/roms/A b.d64" {
		t.Fatalf("args = %q", p.Args)
	}
}

func TestStartTicks(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"1 (a) S 0 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 777 0", 777, true},
		{"1 (a b) c) S 0 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 5", 5, true},
		{"1 (a) S 0 1", 0, false},
		{"no paren", 0, false},
	}
	for _, tc := range cases {
		got, ok := startTicks(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("startTicks(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

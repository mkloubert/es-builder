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
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func newCtl(t *testing.T) *Controller {
	t.Helper()
	return &Controller{RunDir: t.TempDir(), StopTimeout: 2 * time.Second}
}

func write(t *testing.T, c *Controller, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(c.RunDir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(c *Controller, name string) bool {
	_, err := os.Stat(filepath.Join(c.RunDir, name))
	return err == nil
}

// spawn starts a sleep process in its own process group and reaps it in the background.
func spawn(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	return cmd
}

// fakeWrapper writes "stopped" once SIGTERM reached the given process.
func fakeWrapper(c *Controller, cmd *exec.Cmd) {
	go func() {
		for syscall.Kill(cmd.Process.Pid, 0) == nil {
			time.Sleep(10 * time.Millisecond)
		}
		_ = os.WriteFile(filepath.Join(c.RunDir, "state"), []byte("stopped\n"), 0o644)
	}()
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("process %d still alive", pid)
}

func TestStateParsing(t *testing.T) {
	cases := []struct {
		in   string
		want State
	}{
		{"es\n", State{Kind: "es"}},
		{"stopped\n", State{Kind: "stopped"}},
		{"game\tsnes\t/r/x.sfc\n", State{Kind: "game", System: "snes", ROM: "/r/x.sfc"}},
		{"game\tsnes\n", State{Kind: "unknown"}},
		{"bogus\n", State{Kind: "unknown"}},
		{"", State{Kind: "unknown"}},
	}
	for _, tc := range cases {
		c := newCtl(t)
		write(t, c, "state", tc.in)
		if got := c.State(); got != tc.want {
			t.Errorf("%q: got %+v want %+v", tc.in, got, tc.want)
		}
	}
	if got := newCtl(t).State(); got.Kind != "unknown" {
		t.Errorf("missing file: %+v", got)
	}
}

func TestStopCreatesHoldAndWaits(t *testing.T) {
	c := newCtl(t)
	es := spawn(t)
	write(t, c, "state", "es\n")
	write(t, c, "es.pid", fmt.Sprint(es.Process.Pid))
	fakeWrapper(c, es)
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if !exists(c, "hold") {
		t.Error("hold missing")
	}
	if c.State().Kind != "stopped" {
		t.Error("not stopped")
	}
}

func TestStopAlreadyStoppedCreatesHold(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "stopped\n")
	if err := c.Stop(); err != nil || !exists(c, "hold") {
		t.Fatalf("err=%v hold=%v", err, exists(c, "hold"))
	}
}

func TestStopTimeout(t *testing.T) {
	c := newCtl(t)
	c.StopTimeout = 300 * time.Millisecond
	es := spawn(t)
	write(t, c, "state", "es\n")
	write(t, c, "es.pid", fmt.Sprint(es.Process.Pid))
	if err := c.Stop(); !errors.Is(err, ErrStopTimeout) {
		t.Fatalf("got %v", err)
	}
	if exists(c, "hold") {
		t.Error("hold not removed")
	}
}

func TestStartIdempotent(t *testing.T) {
	c := newCtl(t)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	write(t, c, "hold", "")
	if err := c.Start(); err != nil || exists(c, "hold") {
		t.Fatal("hold not removed")
	}
}

func TestRestart(t *testing.T) {
	c := newCtl(t)
	if err := c.Restart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("got %v", err)
	}
	es := spawn(t)
	write(t, c, "es.pid", fmt.Sprint(es.Process.Pid))
	if err := c.Restart(); err != nil {
		t.Fatal(err)
	}
	waitDead(t, es.Process.Pid)
}

func TestLaunchWritesFileExactly(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "es\n")
	write(t, c, "hold", "")
	es := spawn(t)
	write(t, c, "es.pid", fmt.Sprint(es.Process.Pid))
	if err := c.Launch("snes", "/r/x.sfc"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(c.RunDir, "launch"))
	if string(b) != "snes\t/r/x.sfc\n" {
		t.Errorf("got %q", b)
	}
	if exists(c, "hold") {
		t.Error("hold not removed")
	}
	waitDead(t, es.Process.Pid)
}

func TestLaunchRejectsAndGame(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "es\n")
	for _, a := range [][2]string{{"sn\tes", "/r"}, {"snes", "/r\nx"}} {
		if err := c.Launch(a[0], a[1]); err == nil {
			t.Errorf("accepted %q", a)
		}
	}
	if exists(c, "launch") {
		t.Error("launch written")
	}
	write(t, c, "state", "game\tsnes\t/r/x\n")
	if err := c.Launch("snes", "/r/y"); !errors.Is(err, ErrGameRunning) {
		t.Fatalf("got %v", err)
	}
}

func TestWithESStoppedReleasesOnError(t *testing.T) {
	c := newCtl(t)
	es := spawn(t)
	write(t, c, "state", "es\n")
	write(t, c, "es.pid", fmt.Sprint(es.Process.Pid))
	fakeWrapper(c, es)
	boom := errors.New("boom")
	err := c.WithESStopped(context.Background(), func() error {
		if !exists(c, "hold") {
			t.Error("hold missing in fn")
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if exists(c, "hold") {
		t.Error("hold not released")
	}
}

func TestWithESStoppedRefusesDuringGame(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "game\tsnes\t/r/x\n")
	called := false
	err := c.WithESStopped(context.Background(), func() error { called = true; return nil })
	if !errors.Is(err, ErrGameRunning) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestWithESStoppedAlreadyStopped(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "stopped\n")
	write(t, c, "hold", "")
	if err := c.WithESStopped(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !exists(c, "hold") {
		t.Error("hold removed although user stopped ES")
	}
}

func TestWithESStoppedHoldsEvenIfStateStoppedWithoutHold(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "stopped\n") // transient stop: wrapper is about to restart ES
	err := c.WithESStopped(context.Background(), func() error {
		if !exists(c, "hold") {
			t.Error("hold missing during fn")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if exists(c, "hold") {
		t.Error("hold not removed afterwards")
	}
}

func TestWithESStoppedSerializes(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "stopped\n")
	var in, max int32
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			_ = c.WithESStopped(context.Background(), func() error {
				n := atomic.AddInt32(&in, 1)
				if n > atomic.LoadInt32(&max) {
					atomic.StoreInt32(&max, n)
				}
				time.Sleep(20 * time.Millisecond)
				atomic.AddInt32(&in, -1)
				return nil
			})
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	if max != 1 {
		t.Fatalf("max concurrency = %d", max)
	}
}

func TestWithESStoppedContextCancel(t *testing.T) {
	c := newCtl(t)
	c.StopTimeout = 5 * time.Second
	es := spawn(t)
	write(t, c, "state", "es\n")
	write(t, c, "es.pid", fmt.Sprint(es.Process.Pid))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	called := false
	err := c.WithESStopped(ctx, func() error { called = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	if exists(c, "hold") {
		t.Error("hold not removed")
	}
}

func TestStopGameKillsGroup(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "game\tsnes\t/r/x\n")
	if err := c.StopGame(); !errors.Is(err, ErrNoGame) {
		t.Fatalf("got %v", err)
	}
	write(t, c, "state", "game\tsnes\t/r/x\n")
	g := spawn(t)
	write(t, c, "game.pid", fmt.Sprint(g.Process.Pid)+"\n")
	if err := c.StopGame(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-g.Process.Pid, 0); err != syscall.ESRCH {
		t.Errorf("group alive: %v", err)
	}
}

func TestStopGameSigkill(t *testing.T) {
	c := newCtl(t)
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("sh", "-c", "trap '' TERM; touch \"$1\"; exec sleep 30", "sh", ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	for i := 0; i < 200 && !fileExists(ready); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !fileExists(ready) {
		t.Fatal("shell never became ready")
	}
	write(t, c, "state", "game\tsnes\t/r/x\n")
	write(t, c, "game.pid", fmt.Sprint(cmd.Process.Pid))
	start := time.Now()
	if err := c.StopGame(); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 5*time.Second {
		t.Errorf("SIGKILL escalation too early: %v", el)
	}
	if err := syscall.Kill(-cmd.Process.Pid, 0); err != syscall.ESRCH {
		t.Errorf("group alive: %v", err)
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestInvalidPIDsNeverSignal(t *testing.T) {
	for _, v := range []string{"4294967297", "4294967296", "2147483648", "-1", "0", "1", "abc", ""} {
		c := newCtl(t)
		write(t, c, "state", "game\tsnes\t/r/x\n")
		write(t, c, "game.pid", v)
		write(t, c, "es.pid", v)
		if err := c.StopGame(); !errors.Is(err, ErrNoGame) {
			t.Errorf("StopGame %q: %v", v, err)
		}
		if err := c.Restart(); !errors.Is(err, ErrNotRunning) {
			t.Errorf("Restart %q: %v", v, err)
		}
	}
}

func TestStopGameRequiresGameState(t *testing.T) {
	c := newCtl(t)
	g := spawn(t)
	write(t, c, "state", "es\n")
	write(t, c, "game.pid", fmt.Sprint(g.Process.Pid))
	if err := c.StopGame(); !errors.Is(err, ErrNoGame) {
		t.Fatalf("got %v", err)
	}
	if syscall.Kill(g.Process.Pid, 0) != nil {
		t.Error("process was signalled")
	}
}

func TestStopGameRefusesOwnGroupAndGone(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "game\tsnes\t/r/x\n")
	write(t, c, "game.pid", fmt.Sprint(syscall.Getpgrp()))
	if err := c.StopGame(); err == nil || errors.Is(err, ErrNoGame) {
		t.Fatalf("own group: %v", err)
	}
	g := spawn(t)
	_ = syscall.Kill(-g.Process.Pid, syscall.SIGKILL)
	waitDead(t, g.Process.Pid)
	write(t, c, "game.pid", fmt.Sprint(g.Process.Pid))
	if err := c.StopGame(); !errors.Is(err, ErrNoGame) {
		t.Fatalf("gone: %v", err)
	}
}

func TestOpsReturnErrBusyWhileLocked(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "es\n")
	write(t, c, "hold", "")
	c.opMu.Lock()
	defer c.opMu.Unlock()
	checks := map[string]error{
		"Start":   c.Start(),
		"Stop":    c.Stop(),
		"Restart": c.Restart(),
		"Launch":  c.Launch("snes", "/r/x.sfc"),
	}
	for name, err := range checks {
		if !errors.Is(err, ErrBusy) {
			t.Errorf("%s: got %v want ErrBusy", name, err)
		}
	}
	if !exists(c, "hold") {
		t.Error("hold was removed while busy")
	}
	if exists(c, "launch") {
		t.Error("launch file written while busy")
	}
}

func TestStartBlocksDuringWithESStopped(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "stopped\n")
	in, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- c.WithESStopped(context.Background(), func() error { close(in); <-release; return nil })
	}()
	<-in
	if err := c.Start(); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v", err)
	}
	if !exists(c, "hold") {
		t.Error("hold removed mid-operation")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
}

func TestStopRefusesDuringGame(t *testing.T) {
	c := newCtl(t)
	write(t, c, "state", "game\tsnes\t/r/x\n")
	if err := c.Stop(); !errors.Is(err, ErrGameRunning) {
		t.Fatalf("got %v", err)
	}
	if exists(c, "hold") {
		t.Error("hold created")
	}
}

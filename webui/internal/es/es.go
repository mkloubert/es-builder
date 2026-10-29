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

// Package es controls EmulationStation through a file contract with the
// shell wrapper that supervises it (state, hold, launch, es.pid, game.pid
// inside RunDir). The wrapper owns writing the state file.
package es

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrGameRunning = errors.New("es: a game is running")
	ErrStopTimeout = errors.New("es: timeout waiting for EmulationStation to stop")
	ErrNotRunning  = errors.New("es: EmulationStation is not running")
	ErrNoGame      = errors.New("es: no game process known")
	ErrBusy        = errors.New("es: another operation is in progress")
)

const (
	pollInterval    = 100 * time.Millisecond
	gameTermGrace   = 5 * time.Second
	defaultStopWait = 30 * time.Second
	stateES         = "es"
	stateGame       = "game"
	stateStopped    = "stopped"
	stateUnknown    = "unknown"
)

// State is the parsed content of the state file.
type State struct {
	Kind   string // "es" | "game" | "stopped" | "unknown"
	System string
	ROM    string
}

// Controller talks to the ES wrapper via files in RunDir.
type Controller struct {
	RunDir      string
	StopTimeout time.Duration // default 30s

	opMu sync.Mutex // serializes WithESStopped, Start, Stop, Restart, Launch
}

func (c *Controller) path(name string) string { return filepath.Join(c.RunDir, name) }

func (c *Controller) stopTimeout() time.Duration {
	if c.StopTimeout <= 0 {
		return defaultStopWait
	}
	return c.StopTimeout
}

// State reads the state file; missing or malformed content yields "unknown".
func (c *Controller) State() State {
	b, err := os.ReadFile(c.path("state"))
	if err != nil {
		return State{Kind: stateUnknown}
	}
	line := strings.TrimRight(string(b), "\r\n")
	parts := strings.Split(line, "\t")
	switch {
	case len(parts) == 1 && parts[0] == stateES:
		return State{Kind: stateES}
	case len(parts) == 1 && parts[0] == stateStopped:
		return State{Kind: stateStopped}
	case len(parts) == 3 && parts[0] == stateGame && parts[1] != "" && parts[2] != "":
		return State{Kind: stateGame, System: parts[1], ROM: parts[2]}
	}
	return State{Kind: stateUnknown}
}

// readPID returns the PID stored in name, or 0 if missing/invalid.
func (c *Controller) readPID(name string) int {
	b, err := os.ReadFile(c.path(name))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 32)
	if err != nil || n <= 1 || n > pidMax() {
		return 0
	}
	return int(n)
}

// pidMax returns the kernel's pid limit (fallback: 4194304).
func pidMax() int64 {
	b, err := os.ReadFile("/proc/sys/kernel/pid_max")
	if err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 32); err == nil && n > 1 {
			return n
		}
	}
	return 4194304
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// esPID returns the PID of a live ES process or 0.
func (c *Controller) esPID() int {
	pid := c.readPID("es.pid")
	if pid == 0 || !alive(pid) {
		return 0
	}
	return pid
}

// Restart sends SIGTERM to ES; the wrapper starts it again (no hold).
func (c *Controller) Restart() error {
	if !c.opMu.TryLock() {
		return ErrBusy
	}
	defer c.opMu.Unlock()
	pid := c.esPID()
	if pid == 0 {
		return ErrNotRunning
	}
	return signal(pid, syscall.SIGTERM)
}

func signal(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(pid, sig); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

// Stop creates the hold file, terminates ES and waits until state is stopped.
// Returns ErrBusy while a WithESStopped operation (or another call) runs.
func (c *Controller) Stop() error {
	if !c.opMu.TryLock() {
		return ErrBusy
	}
	defer c.opMu.Unlock()
	if c.State().Kind == stateGame {
		return ErrGameRunning
	}
	return c.stop(context.Background())
}

func (c *Controller) stop(ctx context.Context) error {
	if err := c.createHold(); err != nil {
		return err
	}
	if c.State().Kind == stateStopped {
		return nil
	}
	if pid := c.esPID(); pid != 0 {
		if err := signal(pid, syscall.SIGTERM); err != nil {
			_ = c.start()
			return err
		}
	}
	deadline := time.NewTimer(c.stopTimeout())
	defer deadline.Stop()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		if c.State().Kind == stateStopped {
			return nil
		}
		select {
		case <-ctx.Done():
			_ = c.start()
			return ctx.Err()
		case <-deadline.C:
			if c.State().Kind == stateStopped {
				return nil
			}
			_ = c.start()
			return ErrStopTimeout
		case <-tick.C:
		}
	}
}

func (c *Controller) createHold() error {
	f, err := os.OpenFile(c.path("hold"), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// Start removes the hold file so the wrapper may (re)start ES. Idempotent.
// Returns ErrBusy while a WithESStopped operation (or another call) runs.
func (c *Controller) Start() error {
	if !c.opMu.TryLock() {
		return ErrBusy
	}
	defer c.opMu.Unlock()
	return c.start()
}

func (c *Controller) start() error {
	if err := os.Remove(c.path("hold")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Launch asks the wrapper to start a game: writes the launch file atomically,
// clears hold and terminates ES so the wrapper picks the file up.
func (c *Controller) Launch(system, romAbs string) error {
	if !c.opMu.TryLock() {
		return ErrBusy
	}
	defer c.opMu.Unlock()
	for _, s := range []string{system, romAbs} {
		if strings.ContainsAny(s, "\t\n") {
			return fmt.Errorf("es: invalid characters in launch argument %q", s)
		}
	}
	if system == "" || romAbs == "" {
		return errors.New("es: system and rom must not be empty")
	}
	if c.State().Kind == stateGame {
		return ErrGameRunning
	}
	f, err := os.CreateTemp(c.RunDir, "launch.tmp.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.WriteString(system + "\t" + romAbs + "\n")
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o644)
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return werr
	}
	if err := os.Rename(tmp, c.path("launch")); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := c.start(); err != nil {
		return err
	}
	if pid := c.esPID(); pid != 0 {
		return signal(pid, syscall.SIGTERM)
	}
	return nil
}

// StopGame terminates the game's process group, escalating to SIGKILL.
func (c *Controller) StopGame() error {
	if c.State().Kind != stateGame {
		return ErrNoGame
	}
	pgid := c.readPID("game.pid")
	if pgid == 0 {
		return ErrNoGame
	}
	if pgid == syscall.Getpgrp() || pgid == os.Getpid() {
		return fmt.Errorf("es: refusing to signal own process group %d", pgid)
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return ErrNoGame
		}
		return err
	}
	deadline := time.Now().Add(gameTermGrace)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pgid, 0) == syscall.ESRCH {
			return nil
		}
		time.Sleep(pollInterval)
	}
	if err := signal(-pgid, syscall.SIGKILL); err != nil {
		return err
	}
	for i := 0; i < 10; i++ {
		if syscall.Kill(-pgid, 0) == syscall.ESRCH {
			return nil
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("es: game process group %d still exists after SIGKILL", pgid)
}

// WithESStopped runs fn while ES is stopped and the hold file is in place, so
// the wrapper cannot restart ES meanwhile. If hold already existed (the user
// stopped ES), it is left in place afterwards; otherwise it is removed again
// (also when fn fails). Calls are serialized. A running game yields
// ErrGameRunning.
func (c *Controller) WithESStopped(ctx context.Context, fn func() error) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if c.State().Kind == stateGame {
		return ErrGameRunning
	}
	_, herr := os.Stat(c.path("hold"))
	heldBefore := herr == nil
	if err := c.stop(ctx); err != nil {
		if heldBefore {
			_ = c.createHold()
		}
		return err
	}
	fnErr := fn()
	if heldBefore {
		return fnErr
	}
	if startErr := c.start(); fnErr == nil {
		return startErr
	}
	return fnErr
}

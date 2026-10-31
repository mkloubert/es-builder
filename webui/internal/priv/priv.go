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

// Package priv calls the root sudo-helper for privileged system actions.
package priv

import (
	"bytes"
	"context"
	"emulationstation/webui/internal/cpu"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultHelper is the install path of the sudo-helper.
const DefaultHelper = "/usr/local/lib/emulationstation/priv"

const stderrCap = 64 << 10

var allowedUnits = map[string]bool{"emulationstation-web": true, "ssh": true, "fail2ban": true}

// Runner runs an external command, streaming stdout to the writer.
type Runner interface {
	Run(ctx context.Context, stdout io.Writer, name string, args ...string) error
}

// ExecRunner runs commands via os/exec.
type ExecRunner struct{}

type capWriter struct {
	buf bytes.Buffer
	max int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

// Run executes the command; on failure the error includes captured stderr.
func (ExecRunner) Run(ctx context.Context, stdout io.Writer, name string, args ...string) error {
	if stdout == nil {
		stdout = io.Discard
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = stdout
	cmd.WaitDelay = 10 * time.Second
	se := &capWriter{max: stderrCap}
	cmd.Stderr = se
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(se.buf.String()); msg != "" {
			return fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Priv wraps the sudo-helper.
type Priv struct {
	R      Runner
	Helper string
}

// Ban is one fail2ban ban entry.
type Ban struct{ Jail, IP string }

func (p Priv) run(ctx context.Context, w io.Writer, cmd string, arg ...string) error {
	args := append([]string{"-n", p.Helper, cmd}, arg...)
	return p.R.Run(ctx, w, "sudo", args...)
}

func (p Priv) Reboot(ctx context.Context) error   { return p.run(ctx, nil, "reboot") }
func (p Priv) Poweroff(ctx context.Context) error { return p.run(ctx, nil, "poweroff") }

func (p Priv) AptUpdate(ctx context.Context, w io.Writer) error {
	return p.run(ctx, w, "apt-update")
}

func (p Priv) AptUpgrade(ctx context.Context, w io.Writer) error {
	return p.run(ctx, w, "apt-upgrade")
}

// Bans lists current fail2ban bans.
func (p Priv) Bans(ctx context.Context) ([]Ban, error) {
	var buf bytes.Buffer
	if err := p.run(ctx, &buf, "f2b-list"); err != nil {
		return nil, err
	}
	bans := []Ban{}
	for _, line := range strings.Split(buf.String(), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || net.ParseIP(f[1]) == nil {
			continue
		}
		bans = append(bans, Ban{Jail: f[0], IP: f[1]})
	}
	return bans, nil
}

// Unban removes a ban for a valid IP address.
func (p Priv) Unban(ctx context.Context, ip string) error {
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address %q", ip)
	}
	return p.run(ctx, nil, "f2b-unban", ip)
}

// CPUSet sets the top CPU speed (one of cpu.Values); the helper checks the value again.
func (p Priv) CPUSet(ctx context.Context, value string) error {
	if !cpu.ValidValue(value) {
		return fmt.Errorf("invalid cpu speed %q", value)
	}
	return p.run(ctx, nil, "cpu-set", value)
}

var throttledRe = regexp.MustCompile(`^0x[0-9a-fA-F]{1,8}$`)

// CPUThrottled returns the firmware throttle bits ("0x…"); the output is checked again.
func (p Priv) CPUThrottled(ctx context.Context) (string, error) {
	var buf bytes.Buffer
	if err := p.run(ctx, &buf, "cpu-throttled"); err != nil {
		return "", err
	}
	v := strings.TrimSuffix(buf.String(), "\n")
	if !throttledRe.MatchString(v) {
		return "", fmt.Errorf("cpu-throttled: unexpected output %q", buf.String())
	}
	return v, nil
}

// Journal returns the journal of an allowed unit.
func (p Priv) Journal(ctx context.Context, unit string) (string, error) {
	if !allowedUnits[unit] {
		return "", fmt.Errorf("unit %q not allowed", unit)
	}
	var buf bytes.Buffer
	if err := p.run(ctx, &buf, "journal", unit); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ReadESLog returns the last 500 lines of the EmulationStation log.
func ReadESLog(home string) (string, error) {
	b, err := os.ReadFile(filepath.Join(home, ".emulationstation", "es_log.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	s := string(b)
	trimmed := strings.TrimSuffix(s, "\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= 500 {
		return s, nil
	}
	return strings.Join(lines[len(lines)-500:], "\n") + "\n", nil
}

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

// Package bluetooth manages Bluetooth devices through BlueZ. The Manager
// works on the Bus interface so it can be tested without D-Bus.
package bluetooth

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Device is a Bluetooth device known to BlueZ.
type Device struct {
	Address, Name, Icon        string
	Paired, Trusted, Connected bool
	RSSI                       int16
	HasRSSI                    bool
}

// Bus abstracts the BlueZ operations used by the Manager.
type Bus interface {
	HasAdapter() (bool, error)
	PowerOn() error
	StartDiscovery() error
	StopDiscovery() error
	Devices() ([]Device, error)
	Pair(addr string) error
	Trust(addr string) error
	Connect(addr string) error
	Disconnect(addr string) error
	Remove(addr string) error
}

var (
	ErrNoAdapter  = errors.New("no Bluetooth adapter found")
	ErrBadAddress = errors.New("invalid Bluetooth address")
)

// ScanInterval is the update period of Scan; a variable for tests.
var ScanInterval = 2 * time.Second

var addrRE = regexp.MustCompile(`^([0-9A-F]{2}:){5}[0-9A-F]{2}$`)

// ValidAddress reports whether a is a Bluetooth MAC address (any case).
func ValidAddress(a string) bool { return addrRE.MatchString(strings.ToUpper(a)) }

// pairGrace is how long pairing requests are accepted beyond the scan itself
// and while a PairAndConnect runs.
const pairGrace = 60 * time.Second

// PairingWindow tells the D-Bus agent whether pairing requests may currently
// be accepted.
type PairingWindow interface{ Open() bool }

// Window is a deadline-based PairingWindow; the zero value is closed.
type Window struct {
	mu    sync.Mutex
	until time.Time
	now   func() time.Time // nil = time.Now
}

func (w *Window) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

// Allow keeps the window open for d from now (never shortening it).
func (w *Window) Allow(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t := w.clock().Add(d); t.After(w.until) {
		w.until = t
	}
}

// Open reports whether the window is currently open.
func (w *Window) Open() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clock().Before(w.until)
}

// Manager orchestrates Bus calls.
type Manager struct {
	Bus Bus
	win Window
}

// Window returns the pairing window the Manager opens during Scan and
// PairAndConnect; pass it to NewDBus.
func (m *Manager) Window() *Window { return &m.win }

func normalize(a string) (string, error) {
	if !ValidAddress(a) {
		return "", ErrBadAddress
	}
	return strings.ToUpper(a), nil
}

// Devices lists the known devices.
func (m *Manager) Devices() ([]Device, error) {
	devs, err := m.Bus.Devices()
	if err != nil {
		return nil, err
	}
	SortDevices(devs)
	return devs, nil
}

// SortDevices orders by name (case-insensitive), then address.
func SortDevices(devs []Device) {
	sort.SliceStable(devs, func(i, j int) bool {
		a, b := strings.ToLower(devs[i].Name), strings.ToLower(devs[j].Name)
		if a != b {
			return a < b
		}
		return devs[i].Address < devs[j].Address
	})
}

// PairAndConnect pairs (unless already paired), trusts and connects addr.
func (m *Manager) PairAndConnect(ctx context.Context, addr string) error {
	addr, err := normalize(addr)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.requireAdapter(); err != nil {
		return err
	}
	m.win.Allow(pairGrace)
	if err := m.Bus.PowerOn(); err != nil {
		return err
	}
	devs, err := m.Bus.Devices()
	if err != nil {
		return err
	}
	paired := false
	for _, d := range devs {
		if strings.EqualFold(d.Address, addr) && d.Paired {
			paired = true
		}
	}
	if !paired {
		if err := m.Bus.Pair(addr); err != nil {
			return err
		}
	}
	if err := m.Bus.Trust(addr); err != nil {
		return err
	}
	return m.Bus.Connect(addr)
}

func (m *Manager) requireAdapter() error {
	ok, err := m.Bus.HasAdapter()
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoAdapter
	}
	return nil
}

// Disconnect disconnects addr.
func (m *Manager) Disconnect(addr string) error {
	addr, err := normalize(addr)
	if err != nil {
		return err
	}
	return m.Bus.Disconnect(addr)
}

// Remove forgets addr.
func (m *Manager) Remove(addr string) error {
	addr, err := normalize(addr)
	if err != nil {
		return err
	}
	return m.Bus.Remove(addr)
}

// Scan runs discovery for d (or until ctx is done) and reports the device
// list immediately and every ScanInterval. Discovery is always stopped.
func (m *Manager) Scan(ctx context.Context, d time.Duration, onUpdate func([]Device)) (err error) {
	if err := m.requireAdapter(); err != nil {
		return err
	}
	if err := m.Bus.PowerOn(); err != nil {
		return err
	}
	m.win.Allow(d + pairGrace)
	if err := m.Bus.StartDiscovery(); err != nil {
		return err
	}
	defer func() { // not tied to ctx, so it also runs after cancel
		if stopErr := m.Bus.StopDiscovery(); err == nil {
			err = stopErr
		}
	}()

	update := func() {
		if devs, derr := m.Devices(); derr == nil {
			onUpdate(devs)
		}
	}
	update()
	timer := time.NewTimer(d)
	defer timer.Stop()
	tick := time.NewTicker(ScanInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return nil
		case <-tick.C:
			update()
		}
	}
}

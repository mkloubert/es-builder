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

package bluetooth

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type fakeBus struct {
	mu      sync.Mutex
	adapter bool
	devices []Device
	calls   []string
}

func (f *fakeBus) rec(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
	return nil
}
func (f *fakeBus) HasAdapter() (bool, error) { f.rec("HasAdapter"); return f.adapter, nil }
func (f *fakeBus) PowerOn() error            { return f.rec("PowerOn") }
func (f *fakeBus) StartDiscovery() error     { return f.rec("StartDiscovery") }
func (f *fakeBus) StopDiscovery() error      { return f.rec("StopDiscovery") }
func (f *fakeBus) Devices() ([]Device, error) {
	f.rec("Devices")
	return f.devices, nil
}
func (f *fakeBus) Pair(a string) error       { return f.rec("Pair " + a) }
func (f *fakeBus) Trust(a string) error      { return f.rec("Trust " + a) }
func (f *fakeBus) Connect(a string) error    { return f.rec("Connect " + a) }
func (f *fakeBus) Disconnect(a string) error { return f.rec("Disconnect " + a) }
func (f *fakeBus) Remove(a string) error     { return f.rec("Remove " + a) }

const addr = "AA:BB:CC:DD:EE:FF"

func TestPairAndConnectOrder(t *testing.T) {
	f := &fakeBus{adapter: true, devices: []Device{{Address: addr}}}
	m := &Manager{Bus: f}
	if err := m.PairAndConnect(context.Background(), "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatal(err)
	}
	want := []string{"HasAdapter", "PowerOn", "Devices", "Pair " + addr, "Trust " + addr, "Connect " + addr}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestAlreadyPairedSkipsPair(t *testing.T) {
	f := &fakeBus{adapter: true, devices: []Device{{Address: addr, Paired: true}}}
	m := &Manager{Bus: f}
	if err := m.PairAndConnect(context.Background(), addr); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if c == "Pair "+addr {
			t.Fatalf("Pair called: %v", f.calls)
		}
	}
	if f.calls[len(f.calls)-1] != "Connect "+addr {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestInvalidAddressRejected(t *testing.T) {
	f := &fakeBus{adapter: true}
	m := &Manager{Bus: f}
	bad := "../x"
	checks := []error{
		m.PairAndConnect(context.Background(), bad),
		m.Disconnect(bad), m.Remove(bad),
	}
	for i, err := range checks {
		if !errors.Is(err, ErrBadAddress) {
			t.Errorf("%d: %v", i, err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("bus called: %v", f.calls)
	}
}

func TestNoAdapter(t *testing.T) {
	f := &fakeBus{}
	m := &Manager{Bus: f}
	if err := m.PairAndConnect(context.Background(), addr); !errors.Is(err, ErrNoAdapter) {
		t.Fatalf("err %v", err)
	}
}

func TestScanStopsDiscoveryOnCancel(t *testing.T) {
	old := ScanInterval
	ScanInterval = 10 * time.Millisecond
	defer func() { ScanInterval = old }()
	f := &fakeBus{adapter: true, devices: []Device{{Address: addr}}}
	m := &Manager{Bus: f}
	ctx, cancel := context.WithCancel(context.Background())
	updates := 0
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	err := m.Scan(ctx, time.Minute, func([]Device) { updates++ })
	if err != nil {
		t.Fatal(err)
	}
	if updates < 2 {
		t.Errorf("updates %d", updates)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls[len(f.calls)-1] != "StopDiscovery" {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestScanStopsAfterDuration(t *testing.T) {
	old := ScanInterval
	ScanInterval = 10 * time.Millisecond
	defer func() { ScanInterval = old }()
	f := &fakeBus{adapter: true}
	m := &Manager{Bus: f}
	if err := m.Scan(context.Background(), 35*time.Millisecond, func([]Device) {}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls[len(f.calls)-1] != "StopDiscovery" {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestScanSortsDevices(t *testing.T) {
	f := &fakeBus{adapter: true, devices: []Device{
		{Address: "00:00:00:00:00:03", Name: "zed"},
		{Address: "00:00:00:00:00:02", Name: "Alpha"},
		{Address: "00:00:00:00:00:01", Name: "alpha"},
	}}
	m := &Manager{Bus: f}
	var got []Device
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Scan(ctx, time.Minute, func(d []Device) { got = d }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Address != "00:00:00:00:00:01" || got[1].Address != "00:00:00:00:00:02" || got[2].Name != "zed" {
		t.Fatalf("order %+v", got)
	}
}

type stopErrBus struct{ fakeBus }

func (s *stopErrBus) StopDiscovery() error { return errors.New("stop failed") }

func TestScanReturnsStopError(t *testing.T) {
	f := &stopErrBus{fakeBus{adapter: true}}
	m := &Manager{Bus: f}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Scan(ctx, time.Minute, func([]Device) {}); err == nil || err.Error() != "stop failed" {
		t.Fatalf("err %v", err)
	}
}

func TestWindowDeadline(t *testing.T) {
	now := time.Unix(1000, 0)
	w := &Window{now: func() time.Time { return now }}
	if w.Open() {
		t.Fatal("zero window must be closed")
	}
	w.Allow(10 * time.Second)
	now = now.Add(9 * time.Second)
	if !w.Open() {
		t.Fatal("should be open")
	}
	w.Allow(1 * time.Second) // never shortens
	now = now.Add(2 * time.Second)
	if w.Open() {
		t.Fatal("should be closed after deadline")
	}
}

func TestManagerOpensWindowForScanAndPair(t *testing.T) {
	now := time.Unix(1000, 0)
	f := &fakeBus{adapter: true, devices: []Device{{Address: addr}}}
	m := &Manager{Bus: f}
	m.win.now = func() time.Time { return now }
	if m.Window().Open() {
		t.Fatal("closed initially")
	}
	if err := m.Scan(context.Background(), time.Millisecond, func([]Device) {}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(pairGrace)
	if !m.Window().Open() {
		t.Fatal("window must outlive scan by the grace period")
	}
	now = now.Add(time.Second)
	if m.Window().Open() {
		t.Fatal("window must close")
	}
	if err := m.PairAndConnect(context.Background(), addr); err != nil {
		t.Fatal(err)
	}
	if !m.Window().Open() {
		t.Fatal("pairing opens window")
	}
}

func TestAgentRejectsOutsideWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	w := &Window{now: func() time.Time { return now }}
	a := agent{win: w}
	if err := a.RequestConfirmation("/x", 1); err == nil || err.Name != "org.bluez.Error.Rejected" {
		t.Fatalf("got %v", err)
	}
	if _, err := a.RequestPinCode("/x"); err == nil {
		t.Fatal("pin accepted")
	}
	if _, err := a.RequestPasskey("/x"); err == nil {
		t.Fatal("passkey accepted")
	}
	if a.AuthorizeService("/x", "u") == nil || a.RequestAuthorization("/x") == nil {
		t.Fatal("authorize accepted")
	}
	w.Allow(time.Minute)
	if a.RequestConfirmation("/x", 1) != nil || a.AuthorizeService("/x", "u") != nil {
		t.Fatal("rejected inside window")
	}
	if pin, err := a.RequestPinCode("/x"); err != nil || pin != "0000" {
		t.Fatal(pin, err)
	}
}

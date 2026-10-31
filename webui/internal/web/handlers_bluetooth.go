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

package web

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"emulationstation/webui/internal/bluetooth"
)

// scanDuration is how long one Bluetooth scan runs (a variable for tests).
var scanDuration = 30 * time.Second

// pairTimeout bounds one pairing attempt.
const pairTimeout = 45 * time.Second

// btScan tracks the single background scan and its SSE subscribers.
type btScan struct {
	mu      sync.Mutex
	running bool
	last    []bluetooth.Device
	subs    map[chan any]struct{}
}

// begin marks a scan as running; false if one already runs.
func (b *btScan) begin() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return false
	}
	b.running, b.last = true, nil
	return true
}

func (b *btScan) update(devs []bluetooth.Device) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.last = devs
	for ch := range b.subs {
		select { // latest value wins
		case <-ch:
		default:
		}
		select {
		case ch <- devs:
		default:
		}
	}
}

func (b *btScan) finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running = false
	for ch := range b.subs {
		close(ch)
	}
	b.subs = nil
}

// subscribe returns a channel with the current list and later updates; it is
// closed when the scan ends (already closed if none runs).
func (b *btScan) subscribe() (ch chan any, cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch = make(chan any, 1)
	if b.last != nil {
		ch <- b.last
	}
	if !b.running {
		close(ch)
		return ch, func() {}
	}
	if b.subs == nil {
		b.subs = map[chan any]struct{}{}
	}
	b.subs[ch] = struct{}{}
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
	}
}

func (b *btScan) isRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

type btView struct {
	Available bool
	Scanning  bool
	Devices   []bluetooth.Device
	Error     bool
}

func (s *Server) btView() btView {
	v := btView{Available: s.d.BT != nil}
	if !v.Available {
		return v
	}
	v.Scanning = s.scan.isRunning()
	devs, err := s.d.BT.Devices()
	if err != nil {
		s.logf("bluetooth devices: %v", err)
		v.Error = true
	}
	v.Devices = devs
	return v
}

func (s *Server) bluetoothRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /bluetooth/scan", s.handleBTScan)
	mux.HandleFunc("GET /bluetooth/events", s.handleBTEvents)
	mux.HandleFunc("GET /partials/bluetooth", s.handleBTList)
	mux.HandleFunc("POST /bluetooth/{addr}/pair", s.btDevice("pair"))
	mux.HandleFunc("POST /bluetooth/{addr}/disconnect", s.btDevice("disconnect"))
	mux.HandleFunc("POST /bluetooth/{addr}/remove", s.btDevice("remove"))
}

// requireBT answers 409 when no adapter is available.
func (s *Server) requireBT(w http.ResponseWriter, r *http.Request) bool {
	if s.d.BT == nil {
		s.fail(w, r, http.StatusConflict, "No Bluetooth adapter found.")
		return false
	}
	return true
}

func (s *Server) handleBTList(w http.ResponseWriter, r *http.Request) {
	s.partial(w, http.StatusOK, "bt_devices", s.btView())
}

func (s *Server) handleBTScan(w http.ResponseWriter, r *http.Request) {
	if !s.requireBT(w, r) {
		return
	}
	if !s.scan.begin() {
		s.fail(w, r, http.StatusConflict, "A scan is already running.")
		return
	}
	dur := scanDuration
	go func() {
		defer s.scan.finish()
		ctx, cancel := context.WithTimeout(context.Background(), dur+5*time.Second)
		defer cancel()
		if err := s.d.BT.Scan(ctx, dur, s.scan.update); err != nil {
			s.logf("bluetooth scan: %v", err)
		}
	}()
	if isHTMX(r) {
		toastWith(w, "Scan started (30 seconds).", "bt-scan")
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/controllers", http.StatusSeeOther)
}

func (s *Server) handleBTEvents(w http.ResponseWriter, r *http.Request) {
	if !s.requireBT(w, r) {
		return
	}
	ch, cancel := s.scan.subscribe()
	defer cancel()
	s.SSE(w, r, ch, func(v any) (string, error) {
		devs, _ := v.([]bluetooth.Device)
		out := append([]bluetooth.Device(nil), devs...)
		bluetooth.SortDevices(out)
		b, err := s.render("", "bt_devices", btView{Available: true, Scanning: true, Devices: out})
		return string(b), err
	})
}

// btDevice handles pair, disconnect and remove for {addr}.
func (s *Server) btDevice(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireBT(w, r) {
			return
		}
		addr := r.PathValue("addr")
		if !bluetooth.ValidAddress(addr) {
			s.fail(w, r, http.StatusBadRequest, "Invalid Bluetooth address.")
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), pairTimeout)
		defer cancel()
		var err error
		var okMsg, failMsg string
		switch op {
		case "pair":
			err, okMsg, failMsg = s.d.BT.PairAndConnect(ctx, addr), "Controller connected.", "Pairing failed. Is the device in pairing mode?"
		case "disconnect":
			err, okMsg, failMsg = s.d.BT.Disconnect(addr), "Disconnected.", "Disconnect failed."
		default:
			err, okMsg, failMsg = s.d.BT.Remove(addr), "Device removed.", "Remove failed."
		}
		switch {
		case err == nil:
			s.ok(w, r, okMsg, "/controllers", "bt-updated")
		case errors.Is(err, bluetooth.ErrNoAdapter):
			s.fail(w, r, http.StatusConflict, "No Bluetooth adapter found.")
		case errors.Is(err, bluetooth.ErrBadAddress):
			s.fail(w, r, http.StatusBadRequest, "Invalid Bluetooth address.")
		default:
			s.logf("bluetooth %s %s: %v", op, addr, err)
			s.fail(w, r, http.StatusBadGateway, failMsg)
		}
	}
}

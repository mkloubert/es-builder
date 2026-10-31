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
	"emulationstation/webui/internal/cpu"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"emulationstation/webui/internal/es"
	"emulationstation/webui/internal/sysinfo"
	"emulationstation/webui/internal/systems"
)

// statusView is the data of the status partial.
type statusView struct {
	State      es.State
	Session    bool // es-session runs, so the ES buttons work
	ROMName    string
	SystemName string
	Info       sysinfo.Info
	CPU        cpu.State
	Throttle   cpu.Throttle
}

func (s *Server) dashboardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /partials/status", s.handleStatus)
	mux.HandleFunc("POST /es/restart", s.action(s.d.ES.Restart, "EmulationStation is restarting."))
	mux.HandleFunc("POST /es/stop", s.action(s.d.ES.Stop, "EmulationStation was stopped."))
	mux.HandleFunc("POST /es/start", s.action(s.d.ES.Start, "EmulationStation is starting."))
	mux.HandleFunc("POST /game/stop", s.action(s.d.ES.StopGame, "Game stopped."))
}

func (s *Server) status() statusView {
	v := statusView{State: s.d.ES.State(), Session: s.d.ES.SessionRunning()}
	if v.State.Kind == "game" {
		if v.State.ROM != "" {
			v.ROMName = filepath.Base(v.State.ROM)
		}
		v.SystemName = v.State.System
		if s.d.Systems != nil {
			if sys, ok := systems.ByName(s.d.Systems(), v.State.System); ok && sys.FullName != "" {
				v.SystemName = sys.FullName
			}
		}
	}
	v.Info, _ = s.d.Sysinfo.Collect(s.now())
	v.CPU = cpu.Read(s.d.Sysinfo.Root)
	if v.CPU.Supported && s.d.Priv.R != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if raw, err := s.d.Priv.CPUThrottled(ctx); err != nil {
			s.logf("cpu-throttled: %v", err)
		} else {
			v.Throttle = cpu.ParseThrottled(raw)
		}
	}
	return v
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "dashboard", "Dashboard", s.status())
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.partial(w, http.StatusOK, "status", s.status())
}

// actionError maps a controller error to an HTTP status and a message for the user.
func actionError(err error) (int, string) {
	switch {
	case errors.Is(err, es.ErrNoSession):
		return http.StatusConflict, "EmulationStation is not running on the device. Start it on the device screen."
	case errors.Is(err, es.ErrUnmanaged):
		return http.StatusConflict, `EmulationStation runs, but not in its session. Quit it on the device, then start it again with "emulationstation" on the device screen.`
	case errors.Is(err, es.ErrStopTimeout):
		return http.StatusGatewayTimeout, "EmulationStation did not stop in time."
	case errors.Is(err, es.ErrNotRunning):
		return http.StatusConflict, "EmulationStation is not running."
	case errors.Is(err, es.ErrNoGame):
		return http.StatusConflict, "No game is running."
	case errors.Is(err, es.ErrBusy):
		return http.StatusConflict, "Please wait – a change is in progress."
	case errors.Is(err, es.ErrGameRunning):
		return http.StatusConflict, "A game is running right now."
	}
	return http.StatusInternalServerError, "Action failed."
}

// action wraps a controller call as POST handler: htmx gets the refreshed
// status partial plus a toast, plain browsers a 303 redirect to the dashboard.
func (s *Server) action(fn func() error, okMsg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn()
		if err != nil {
			code, msg := actionError(err)
			s.logf("%s %s: %v", r.Method, r.URL.Path, err)
			if isHTMX(r) {
				toast(w, msg)
				w.Header().Set("HX-Reswap", "none")
				w.WriteHeader(code)
				return
			}
			http.Error(w, msg, code)
			return
		}
		if isHTMX(r) {
			toast(w, okMsg)
			s.partial(w, http.StatusOK, "status", s.status())
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

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

// Package web contains the HTTPS server, middleware chain and page handlers.
package web

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"emulationstation/webui/internal/auth"
	"emulationstation/webui/internal/bluetooth"
	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/es"
	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/priv"
	"emulationstation/webui/internal/roms"
	"emulationstation/webui/internal/sysinfo"
	"emulationstation/webui/internal/systems"
	"emulationstation/webui/internal/usb"
)

// Deps are the collaborators of the web handlers.
type Deps struct {
	ConfigPath string
	Config     func() *config.Config
	SetConfig  func(*config.Config) error
	// UpdateConfig applies fn to a copy of the config and saves it atomically.
	UpdateConfig func(fn func(*config.Config) error) error
	Systems      func() []systems.System
	Stager       *roms.Stager
	ES           *es.Controller
	Jobs         *jobs.Queue
	Priv         priv.Priv
	Sysinfo      sysinfo.Collector
	BT           *bluetooth.Manager // nil = no adapter
	USB          *usb.Importer      // nil = no USB import
	Runner       priv.Runner
	LogW         io.Writer
	SysRoot      string
	MountInfo    string // mount list to check for a mounted USB stick; "" = /proc/self/mountinfo
}

// Server holds the handler state.
type Server struct {
	d   Deps
	tpl atomic.Pointer[templates] // swapped by the reload button
	ui  atomic.Pointer[uiState]   // custom web UI state of the loaded templates
	mu  sync.Mutex                // serializes log writes
	now func() time.Time

	changes  *Changes    // pending rename/delete changes of the ROM library
	powering atomic.Bool // a reboot/poweroff is pending
	scan     btScan      // the single background Bluetooth scan
	usbMu    sync.Mutex  // makes "is a USB import running?" and the submit (or the eject) one step
}

func (s *Server) logf(format string, a ...any) {
	if s.d.LogW == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.d.LogW, format+"\n", a...)
}

// securityHeaders sets the headers every response carries.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// NewHandler builds the full handler chain:
// gzip -> security headers -> CrossOriginProtection -> auth.Middleware -> mux,
// so that 401/403 responses carry the headers too.
// It panics if the embedded templates are broken (a build-time bug).
func NewHandler(d Deps) http.Handler {
	_, h := newServer(d)
	return h
}

// newServer is NewHandler that also returns the server (for tests).
func newServer(d Deps) (*Server, http.Handler) {
	s := &Server{d: d, now: time.Now, changes: &Changes{}}
	s.loadTemplates()
	mux := http.NewServeMux()
	s.routes(mux)
	return s, gzipHandler(securityHeaders(http.NewCrossOriginProtection().Handler(auth.Middleware(d.Config, d.LogW, mux))))
}

func (s *Server) routes(mux *http.ServeMux) {
	static, err := fs.Sub(newOverlayFS(s.uiDir(), assets), "static")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(static)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") { // no directory listings
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "max-age=86400")
		files.ServeHTTP(w, r)
	})))
	s.dashboardRoutes(mux)
	s.aboutRoutes(mux)
	s.gamesRoutes(mux)
	s.uploadRoutes(mux)
	s.usbRoutes(mux)
	s.biosRoutes(mux)
	s.controllerRoutes(mux)
	s.systemRoutes(mux)
	s.settingsRoutes(mux)
}

func tlsConfig() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS12} }

// ListenAndServeTLS serves h on cfg.Listen until ctx is cancelled, then shuts
// down gracefully (10 s).
func ListenAndServeTLS(ctx context.Context, cfg *config.Config, h http.Handler) error {
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           h,
		TLSConfig:         tlsConfig(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		// long-lived handlers (SSE) see the shutdown through their request context
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
		return err
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

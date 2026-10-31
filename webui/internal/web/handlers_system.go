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
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/priv"
)

// powerDelay is the pause between answering a reboot/poweroff request and
// executing it; a variable for tests.
var powerDelay = time.Second

func (s *Server) systemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /system", s.handleSystemPage)
	mux.HandleFunc("GET /system/log", s.handleSystemLog)
	mux.HandleFunc("GET /partials/bans", s.handleBansPartial)
	mux.HandleFunc("POST /system/unban", s.handleUnban)
	mux.HandleFunc("POST /system/update", s.aptJob("Update package lists", priv.Priv.AptUpdate))
	mux.HandleFunc("POST /system/upgrade", s.aptJob("Upgrade packages", priv.Priv.AptUpgrade))
	mux.HandleFunc("POST /system/reboot", s.power("The system is restarting …", priv.Priv.Reboot))
	mux.HandleFunc("POST /system/poweroff", s.power("The system is shutting down …", priv.Priv.Poweroff))
	mux.HandleFunc("POST /system/ui-reload", s.handleUIReload)
}

type logUnit struct{ Key, Label, Unit string }

var logUnits = []logUnit{
	{"web", "Web service", "emulationstation-web"},
	{"ssh", "SSH", "ssh"},
	{"fail2ban", "fail2ban", "fail2ban"},
	{"es", "EmulationStation", ""},
}

type bansView struct {
	Bans  []priv.Ban
	Error bool
}

type sysPageView struct {
	Units []logUnit
	Bans  bansView
	UI    uiState
}

func (s *Server) bans(ctx context.Context) bansView {
	if s.d.Priv.R == nil {
		return bansView{Error: true}
	}
	bans, err := s.d.Priv.Bans(ctx)
	if err != nil {
		s.logf("bans: %v", err)
		return bansView{Error: true}
	}
	return bansView{Bans: bans}
}

func (s *Server) handleSystemPage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "system", "System", sysPageView{Units: logUnits, Bans: s.bans(r.Context()), UI: *s.ui.Load()})
}

// handleUIReload parses the templates again (custom folder over the built-in files).
func (s *Server) handleUIReload(w http.ResponseWriter, r *http.Request) {
	st := s.loadTemplates()
	if !isHTMX(r) {
		redirect(w, r, "/system")
		return
	}
	if st.Err != "" {
		toast(w, "Custom templates have an error. The built-in web UI is in use.")
	} else {
		toast(w, "Web UI reloaded.")
	}
	s.partial(w, http.StatusOK, "custom_ui", st)
}

func (s *Server) handleBansPartial(w http.ResponseWriter, r *http.Request) {
	s.partial(w, http.StatusOK, "bans", s.bans(r.Context()))
}

type logView struct {
	Label, Text string
	Error       bool
}

func (s *Server) handleSystemLog(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("unit")
	for _, u := range logUnits {
		if u.Key != key {
			continue
		}
		v := logView{Label: u.Label}
		var err error
		if u.Unit == "" {
			v.Text, err = priv.ReadESLog(s.d.Config().Paths.Home)
		} else if s.d.Priv.R == nil {
			err = io.ErrClosedPipe
		} else {
			v.Text, err = s.d.Priv.Journal(r.Context(), u.Unit)
		}
		if err != nil {
			s.logf("log %s: %v", key, err)
			v.Error = true
		}
		s.partial(w, http.StatusOK, "syslog", v)
		return
	}
	s.fail(w, r, http.StatusNotFound, "Unknown log source.")
}

func (s *Server) handleUnban(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	ip := r.PostFormValue("ip")
	if net.ParseIP(ip) == nil {
		s.fail(w, r, http.StatusBadRequest, "Invalid IP address.")
		return
	}
	if err := s.d.Priv.Unban(context.WithoutCancel(r.Context()), ip); err != nil {
		s.logf("unban %s: %v", ip, err)
		s.fail(w, r, http.StatusInternalServerError, "Unban failed.")
		return
	}
	s.ok(w, r, ip+" was unbanned.", "/system", "bans-updated")
}

// lineWriter forwards complete output lines to a job log.
type lineWriter struct {
	rep jobs.Reporter
	buf bytes.Buffer
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.buf.Write(p)
	for {
		i := bytes.IndexByte(l.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		l.rep.Logf("%s", bytes.TrimRight(l.buf.Next(i+1), "\r\n"))
	}
	return len(p), nil
}

func (l *lineWriter) flush() {
	if l.buf.Len() > 0 {
		l.rep.Logf("%s", l.buf.String())
		l.buf.Reset()
	}
}

func (s *Server) aptJob(title string, run func(priv.Priv, context.Context, io.Writer) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.d.Jobs == nil {
			s.fail(w, r, http.StatusInternalServerError, "Job queue is not available.")
			return
		}
		p := s.d.Priv
		job := s.d.Jobs.Submit("apt", title, func(ctx context.Context, rep jobs.Reporter) error {
			lw := &lineWriter{rep: rep}
			defer lw.flush()
			return run(p, ctx, lw)
		})
		redirect(w, r, "/jobs/"+job.ID)
	}
}

// power answers first and performs the action after powerDelay.
func (s *Server) power(msg string, act func(priv.Priv, context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.powering.CompareAndSwap(false, true) {
			s.fail(w, r, http.StatusConflict, "Already running.")
			return
		}
		delay, p := powerDelay, s.d.Priv
		go func() {
			time.Sleep(delay)
			if err := act(p, context.Background()); err != nil {
				s.logf("power action: %v", err)
				s.powering.Store(false) // failed: allow a retry
			}
		}()
		s.ok(w, r, msg, "/system")
	}
}

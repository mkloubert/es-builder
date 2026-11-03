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
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/update"
)

// installStatusFile is written by the root install script in the run folder.
const installStatusFile = "webui-update.status"

// ReadInstallStatus <runDir> : reads and removes the status line of the last
// web UI install ("ok|rolled-back|failed <x.y.z>"). found is false when there
// is no file or the line is not valid. Any word other than "ok" means failure.
func ReadInstallStatus(runDir string) (ok bool, v string, found bool) {
	p := filepath.Join(runDir, installStatusFile)
	b, err := os.ReadFile(p)
	if err != nil {
		return false, "", false
	}
	_ = os.Remove(p)
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return false, "", false
	}
	if _, valid := update.ParseVersion(f[1]); !valid {
		return false, "", false
	}
	return f[0] == "ok", f[1], true
}

// installRunDir returns the folder of the install status file.
func (s *Server) installRunDir() string {
	if s.d.ES != nil && s.d.ES.RunDir != "" {
		return s.d.ES.RunDir
	}
	if c := s.d.Config(); c != nil {
		return c.Paths.RunDir
	}
	return ""
}

// takeFlash looks for the install status file on every page view. The install
// script writes it after the restart. The first view that finds it removes it
// and gets the text, so it is shown once.
func (s *Server) takeFlash() string {
	dir := s.installRunDir()
	if dir == "" {
		return ""
	}
	s.flashMu.Lock()
	defer s.flashMu.Unlock()
	ok, v, found := ReadInstallStatus(dir)
	switch {
	case !found:
		return ""
	case ok:
		return "Web UI updated to " + v + "."
	}
	return "The update failed. The old version runs again."
}

// updateJobBusy reports a webui-update job that is queued or running.
func (s *Server) updateJobBusy() bool {
	if s.d.Jobs == nil {
		return false
	}
	for _, j := range s.d.Jobs.List() {
		if j.Kind == "webui-update" && (j.Status == jobs.StatusQueued || j.Status == jobs.StatusRunning) {
			return true
		}
	}
	return false
}

// updateBusy reports a running or queued web UI update.
func (s *Server) updateBusy() bool { return s.d.Update.Busy() || s.updateJobBusy() }

// updateOn tells if self-update is set up.
func (s *Server) updateOn() bool {
	return s.d.Update != nil && s.d.Checker != nil
}

// runningVersion returns the running web UI version (0.0.0 if it is not x.y.z).
func (s *Server) runningVersion() update.Version {
	v, _ := update.ParseVersion(s.d.Version)
	return v
}

// readyVersion returns the newer version found by the last check, or "".
func (s *Server) readyVersion() string {
	if !s.updateOn() {
		return ""
	}
	if v, ok := s.d.Checker.Load().Newer(s.runningVersion()); ok {
		return v.String()
	}
	return ""
}

func (s *Server) updateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /system/webui-update/check", s.handleUpdateCheck)
	mux.HandleFunc("POST /system/webui-update/run", s.handleUpdateRun)
}

// handleHealthz answers the install script. It is open to the loopback only.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		http.NotFound(w, r)
		return
	}
	b, _ := json.Marshal(map[string]string{"version": s.d.Version})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(append(b, '\n'))
}

// handleUpdateCheck asks the update source now. It runs in the request: the
// git calls have their own time limits (about 60 s at most).
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !s.updateOn() {
		s.fail(w, r, http.StatusConflict, "Self-update is off.")
		return
	}
	if s.updateBusy() {
		s.fail(w, r, http.StatusConflict, "An update is running.")
		return
	}
	s.d.Checker.Check(r.Context())
	if isHTMX(r) {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/system#webui", http.StatusSeeOther)
}

// handleUpdateRun starts the update job for the version found by the last check.
func (s *Server) handleUpdateRun(w http.ResponseWriter, r *http.Request) {
	if !s.updateOn() {
		s.fail(w, r, http.StatusConflict, "Self-update is off.")
		return
	}
	if s.d.Jobs == nil {
		s.fail(w, r, http.StatusInternalServerError, "Job queue is not available.")
		return
	}
	// The lock makes the busy check and the submit one step.
	s.updMu.Lock()
	defer s.updMu.Unlock()
	if s.updateBusy() {
		s.fail(w, r, http.StatusConflict, "An update is running.")
		return
	}
	v, ok := s.d.Checker.Load().Newer(s.runningVersion())
	if !ok {
		s.fail(w, r, http.StatusConflict, "There is no newer version. Use Check now first.")
		return
	}
	up := s.d.Update
	job := s.d.Jobs.Submit("webui-update", "Update web UI to "+v.String(), func(ctx context.Context, rep jobs.Reporter) error {
		err := up.Run(ctx, rep, v)
		if err != nil {
			s.logf("web ui update to %s: %v", v, err)
			return errors.New(update.Message(err)) // never the raw error
		}
		return nil
	})
	redirect(w, r, "/jobs/"+job.ID)
}

// webUIView is the data of the "Web UI update" panel.
type webUIView struct {
	On       bool
	Version  string
	Source   string
	Checked  string // "" = never
	Error    string // short text for the last error, or ""
	Newer    string // newer version, or ""
	Unsigned bool   // no signers file: tags are not checked
}

// updateErrorText maps a check error code to a short text.
func updateErrorText(code string) string {
	switch code {
	case "":
		return ""
	case update.ErrCodeAuth:
		return "The login for the update source failed."
	case update.ErrCodeNoTags:
		return "The update source has no versions yet."
	case update.ErrCodeBadURL:
		return "The update source address is not valid."
	}
	return "The update source cannot be reached."
}

// webUIView builds the data of the panel.
func (s *Server) webUIView() webUIView {
	v := webUIView{Version: s.d.Version}
	if !s.updateOn() {
		return v
	}
	cfg := s.d.Config()
	st := s.d.Checker.Load()
	v.On = true
	v.Source = cfg.UpdateURL
	v.Unsigned = cfg.UpdateSignersFile == ""
	v.Error = updateErrorText(st.Err)
	if !st.CheckedAt.IsZero() {
		v.Checked = st.CheckedAt.Local().Format("2006-01-02 15:04")
	}
	v.Newer = s.readyVersion()
	return v
}

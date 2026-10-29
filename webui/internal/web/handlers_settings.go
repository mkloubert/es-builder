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
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"emulationstation/webui/internal/config"
)

const minPasswordLen = 4

func (s *Server) settingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings/password", s.handlePassword)
	mux.HandleFunc("POST /settings/screenscraper", s.handleScreenscraper)
}

type settingsView struct {
	WebUser, SSUser string
	SSPassSet       bool
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	c := s.d.Config()
	s.page(w, r, "settings", "Settings", settingsView{WebUser: c.WebUser, SSUser: c.ScreenscraperUser, SSPassSet: c.ScreenscraperPass != ""})
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

func (s *Server) update(w http.ResponseWriter, r *http.Request, fn func(*config.Config)) bool {
	if s.d.UpdateConfig == nil {
		s.fail(w, r, http.StatusInternalServerError, "Settings cannot be saved.")
		return false
	}
	if err := s.d.UpdateConfig(func(c *config.Config) error { fn(c); return nil }); err != nil {
		s.logf("save config: %v", err)
		s.fail(w, r, http.StatusInternalServerError, "Settings could not be saved.")
		return false
	}
	return true
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	user := strings.TrimSpace(r.PostFormValue("user"))
	pw, pw2 := r.PostFormValue("password"), r.PostFormValue("password2")
	switch {
	case user == "" || pw == "":
		s.fail(w, r, http.StatusBadRequest, "User name and password must not be empty.")
		return
	case strings.Contains(user, ":"):
		s.fail(w, r, http.StatusBadRequest, "The user name must not contain a colon.")
		return
	case hasControl(user) || hasControl(pw):
		s.fail(w, r, http.StatusBadRequest, "Invalid characters in user name or password.")
		return
	case pw != pw2:
		s.fail(w, r, http.StatusBadRequest, "The passwords do not match.")
		return
	case utf8.RuneCountInString(pw) < minPasswordLen:
		s.fail(w, r, http.StatusBadRequest, "The password must be at least 4 characters long.")
		return
	}
	hash, err := config.HashPassword(pw)
	if err != nil { // bcrypt rejects passwords over 72 bytes
		s.fail(w, r, http.StatusBadRequest, "The password is invalid or too long (72 bytes at most).")
		return
	}
	if !s.update(w, r, func(c *config.Config) { c.WebUser, c.WebPasswordHash = user, hash }) {
		return
	}
	// The new credentials are active now; the old ones yield 401, so the
	// browser prompts again when it loads the dashboard.
	redirect(w, r, "/")
}

func (s *Server) handleScreenscraper(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	user, pass := strings.TrimSpace(r.PostFormValue("user")), r.PostFormValue("pass")
	if hasControl(user) || hasControl(pass) {
		s.fail(w, r, http.StatusBadRequest, "Invalid characters in the credentials.")
		return
	}
	if !s.update(w, r, func(c *config.Config) {
		c.ScreenscraperUser = user
		if pass != "" { // an empty field keeps the stored password
			c.ScreenscraperPass = pass
		}
	}) {
		return
	}
	s.ok(w, r, "ScreenScraper credentials saved.", "/settings")
}

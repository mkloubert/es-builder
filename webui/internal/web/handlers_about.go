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

	"emulationstation/webui/internal/legal"
)

// aboutRow is one third-party component with its license text.
type aboutRow struct {
	legal.Component
	Text string
}

// aboutView is the data of the about page.
type aboutView struct {
	License    string
	Components []aboutRow
}

// aboutRoutes <mux> : registers the read only about page.
func (s *Server) aboutRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /about", s.handleAbout)
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	v := aboutView{License: legal.LicenseText()}
	for _, c := range legal.Components {
		text, err := legal.ComponentLicense(c)
		if err != nil {
			s.logf("about: license of %s: %v", c.Name, err)
			text = "The license text could not be read."
		}
		v.Components = append(v.Components, aboutRow{Component: c, Text: text})
	}
	s.page(w, r, "about", "About & licenses", v)
}

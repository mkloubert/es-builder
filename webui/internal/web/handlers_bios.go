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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"emulationstation/webui/internal/bios"
	"emulationstation/webui/internal/roms"
	"emulationstation/webui/internal/systems"
)

func (s *Server) biosRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /bios", s.handleBios)
	mux.HandleFunc("POST /bios", s.handleBiosUpload)
}

type biosRow struct {
	File     string
	Optional bool
	State    string // ok | missing | mismatch
}

type biosGroup struct {
	Name string
	Rows []biosRow
}

type biosView struct {
	Groups []biosGroup
	Notice string
}

func (s *Server) handleBios(w http.ResponseWriter, r *http.Request) {
	v := biosView{}
	if n, err := strconv.Atoi(r.URL.Query().Get("saved")); err == nil && n > 0 {
		if n == 1 {
			v.Notice = "1 BIOS file was saved."
		} else {
			v.Notice = fmt.Sprintf("%d BIOS files were saved.", n)
		}
	}
	idx := map[string]int{}
	for _, st := range bios.Check(s.d.Config().Paths.BIOS) {
		i, ok := idx[st.System]
		if !ok {
			name := st.System
			if sys, found := systems.ByName(s.systemList(), st.System); found && sys.FullName != "" {
				name = sys.FullName
			}
			v.Groups = append(v.Groups, biosGroup{Name: name})
			i = len(v.Groups) - 1
			idx[st.System] = i
		}
		v.Groups[i].Rows = append(v.Groups[i].Rows, biosRow{File: st.File, Optional: st.Optional, State: st.State})
	}
	s.page(w, r, "bios", "BIOS", v)
}

// dreamcastFile reports names that belong into the dc/ subfolder.
func dreamcastFile(name string) bool { return name == "dc_boot.bin" || name == "dc_flash.bin" }

// handleBiosUpload streams every file part into the BIOS directory. A text
// field "target" (value "dc") sent before the files selects the Dreamcast
// subfolder; dc_boot.bin and dc_flash.bin always go there.
func (s *Server) handleBiosUpload(w http.ResponseWriter, r *http.Request) {
	dir := s.d.Config().Paths.BIOS
	r.Body = http.MaxBytesReader(w, r.Body, 16*bios.MaxSize)
	mr, err := r.MultipartReader()
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Invalid upload request.")
		return
	}
	target, saved := "", 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.fail(w, r, http.StatusBadRequest, "Invalid upload request.")
			return
		}
		if part.FileName() == "" {
			if part.FormName() == "target" {
				b, _ := io.ReadAll(io.LimitReader(part, 16))
				target = string(b)
			}
			continue
		}
		name, err := roms.SafeName(part.FileName())
		if err != nil {
			s.fail(w, r, http.StatusBadRequest, "Unsafe file name.")
			return
		}
		if target == "dc" || dreamcastFile(name) {
			name = "dc/" + name
		}
		if err := bios.Save(dir, name, part); err != nil {
			s.logf("bios %q: %v", name, err)
			switch {
			case isNoSpace(err):
				s.fail(w, r, http.StatusInsufficientStorage, msgNoSpace)
			case errors.Is(err, bios.ErrTooLarge):
				s.fail(w, r, http.StatusRequestEntityTooLarge, "The BIOS file is too large (64 MiB at most).")
			case errors.Is(err, roms.ErrUnsafePath):
				s.fail(w, r, http.StatusBadRequest, "Unsafe file name.")
			default:
				s.fail(w, r, http.StatusInternalServerError, "The BIOS file could not be saved.")
			}
			return
		}
		saved++
	}
	if saved == 0 {
		s.fail(w, r, http.StatusBadRequest, "No file was sent.")
		return
	}
	redirect(w, r, "/bios?saved="+strconv.Itoa(saved))
}

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

// Package systems reads the RetroPie es_systems.cfg.
package systems

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// System is one emulated platform.
type System struct {
	Name, FullName, Path string
	Extensions           []string // lowercase, with dot, deduplicated
	KeepArchives         bool     // archive is the ROM itself; never unpack
}

var keepArchives = map[string]bool{
	"arcade": true, "fba": true, "fbneo": true, "neogeo": true,
	"mame-libretro": true, "mame-mame4all": true, "mame-advmame": true,
}

type xmlSystem struct {
	Name      string `xml:"name"`
	FullName  string `xml:"fullname"`
	Path      string `xml:"path"`
	Extension string `xml:"extension"`
}

type xmlList struct {
	Systems []xmlSystem `xml:"system"`
}

// Load parses the es_systems.cfg at path.
func Load(path string) ([]System, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var l xmlList
	if err := xml.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	out := make([]System, 0, len(l.Systems))
	for _, x := range l.Systems {
		name := strings.TrimSpace(x.Name)
		s := System{
			Name:         name,
			FullName:     strings.TrimSpace(x.FullName),
			Path:         strings.TrimSpace(x.Path),
			KeepArchives: keepArchives[name],
		}
		seen := map[string]bool{}
		for _, e := range strings.Fields(x.Extension) {
			e = strings.ToLower(e)
			if !seen[e] {
				seen[e] = true
				s.Extensions = append(s.Extensions, e)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// ByName returns the system with the given name.
func ByName(list []System, name string) (System, bool) {
	for _, s := range list {
		if s.Name == name {
			return s, true
		}
	}
	return System{}, false
}

// Candidates returns the systems accepting filename's extension
// (case-insensitive), sorted by name.
func Candidates(list []System, filename string) []System {
	ext := strings.ToLower(filepath.Ext(filename))
	out := []System{}
	if ext == "" {
		return out
	}
	for _, s := range list {
		for _, e := range s.Extensions {
			if e == ext {
				out = append(out, s)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

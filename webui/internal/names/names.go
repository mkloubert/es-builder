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

// Package names derives readable display names from ROM file names.
package names

import (
	"regexp"
	"strings"
)

var (
	extRe     = regexp.MustCompile(`\.[A-Za-z0-9]{1,4}$`)
	discRe    = regexp.MustCompile(`(?i)\(\s*dis[ck]\s*(\d+)[^)]*\)`)
	bracketRe = regexp.MustCompile(`\([^)]*\)|\[[^\]]*\]`)
	theRe     = regexp.MustCompile(`^(.*?), The( - .*)?$`)
	spaceRe   = regexp.MustCompile(`\s+`)
)

// Clean turns a ROM file name into a display name.
func Clean(filename string) string {
	base := extRe.ReplaceAllString(filename, "")
	s := base

	disc := ""
	if m := discRe.FindStringSubmatch(s); m != nil {
		disc = m[1]
	}
	s = bracketRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "_", " ")
	s = spaceRe.ReplaceAllString(strings.TrimSpace(s), " ")
	if !strings.Contains(s, " ") {
		s = strings.ReplaceAll(s, ".", " ")
	}
	if m := theRe.FindStringSubmatch(s); m != nil {
		s = "The " + m[1] + m[2]
	}
	s = strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	if s == "" {
		return base
	}
	if disc != "" {
		s += " – Disc " + disc
	}
	return s
}

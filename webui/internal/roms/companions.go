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

package roms

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxDescriptorBytes = 1 << 20

var descriptorExts = map[string]bool{".cue": true, ".gdi": true, ".m3u": true, ".ccd": true}

func isDescriptor(name string) bool {
	return descriptorExts[strings.ToLower(filepath.Ext(name))]
}

// tokens splits a line into whitespace-separated fields; double quotes group.
func tokens(line string) []string {
	var out []string
	var cur strings.Builder
	inQ, has := false, false
	for _, r := range line {
		switch {
		case r == '"':
			inQ = !inQ
			has = true
		case !inQ && (r == ' ' || r == '\t'):
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

func readLines(p string) ([]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(io.LimitReader(f, maxDescriptorBytes))
	sc.Buffer(make([]byte, 64*1024), maxDescriptorBytes)
	for sc.Scan() {
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff")))
	}
	return lines, sc.Err()
}

// references returns the raw referenced file names of a descriptor file.
func references(primaryPath string) ([]string, error) {
	ext := strings.ToLower(filepath.Ext(primaryPath))
	if ext == ".ccd" {
		stem := strings.TrimSuffix(filepath.Base(primaryPath), filepath.Ext(primaryPath))
		return []string{stem + ".img", stem + ".sub"}, nil
	}
	lines, err := readLines(primaryPath)
	if err != nil {
		return nil, err
	}
	var refs []string
	switch ext {
	case ".cue":
		for _, l := range lines {
			t := tokens(l)
			if len(t) >= 2 && strings.EqualFold(t[0], "FILE") {
				refs = append(refs, t[1])
			}
		}
	case ".gdi":
		for i, l := range lines {
			if i == 0 {
				continue
			}
			if t := tokens(l); len(t) >= 5 {
				refs = append(refs, t[4])
			}
		}
	case ".m3u":
		for _, l := range lines {
			if l != "" && !strings.HasPrefix(l, "#") {
				refs = append(refs, l)
			}
		}
	}
	return refs, nil
}

// References returns the file names that a descriptor file lists, as they
// are written in it (they may have folders, with "/" or a backslash). A .ccd
// file lists <stem>.img and <stem>.sub.
func References(primaryPath string) ([]string, error) {
	return references(primaryPath)
}

// Companions returns the base names of the existing files in the primary's
// directory that the descriptor (.cue/.gdi/.m3u/.ccd) refers to. References
// are resolved by base name; missing files are ignored.
func Companions(primaryPath string) ([]string, error) {
	if !isDescriptor(primaryPath) {
		return nil, nil
	}
	refs, err := references(primaryPath)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(primaryPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	exact := map[string]string{}
	folded := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		exact[e.Name()] = e.Name()
		if _, ok := folded[strings.ToLower(e.Name())]; !ok {
			folded[strings.ToLower(e.Name())] = e.Name()
		}
	}
	self := filepath.Base(primaryPath)
	seen := map[string]bool{}
	out := []string{}
	for _, ref := range refs {
		base := ref
		if i := strings.LastIndexAny(base, `/\`); i >= 0 {
			base = base[i+1:]
		}
		name, ok := exact[base]
		if !ok {
			name, ok = folded[strings.ToLower(base)]
		}
		if !ok || name == self || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// allCompanions resolves companions transitively (m3u -> cue -> bin).
func allCompanions(primaryPath string) ([]string, error) {
	dir := filepath.Dir(primaryPath)
	seen := map[string]bool{filepath.Base(primaryPath): true}
	var out []string
	queue := []string{primaryPath}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		cs, err := Companions(p)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
			queue = append(queue, filepath.Join(dir, c))
		}
	}
	return out, nil
}

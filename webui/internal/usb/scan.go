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

package usb

import (
	"errors"
	"io/fs"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"emulationstation/webui/internal/roms"
)

// ErrTooManyFiles is returned by Scan when there are more files than allowed.
var ErrTooManyFiles = errors.New("There are too many files on the USB stick.")

// Skip reasons of Scan.
const (
	reasonSymlink   = "symlink"
	reasonUnsafe    = "unsafe name"
	reasonReadError = "read error"
	reasonNotFile   = "not a regular file"
)

// Group is the files of one first-level folder below roms/ (Hint is the
// folder name), or the loose files directly in roms/ (Hint "").
type Group struct {
	Hint  string
	Dir   string   // folder the Rels are relative to
	Rels  []string // slash paths below Dir
	Bytes int64    // sum of the file sizes
}

// Skip is a file or folder that Scan does not import.
type Skip struct{ Rel, Reason string } // Rel is a slash path below the roms folder

var junkNames = map[string]bool{
	".ds_store": true, "thumbs.db": true, "desktop.ini": true,
	"system volume information": true, "$recycle.bin": true, ".trashes": true,
	".spotlight-v100": true, ".fseventsd": true,
}

// isJunk reports system files of PCs. They are ignored silently. (The
// device's own README.txt and import-report.txt are in the base folder, which
// is never scanned.)
func isJunk(name string) bool {
	return strings.HasPrefix(name, "._") || junkNames[strings.ToLower(name)]
}

// safeName reports a file name that roms.SafeName keeps as it is.
func safeName(name string) bool {
	b, err := roms.SafeName(name)
	return err == nil && b == name
}

// Scan walks romsDir. It does not follow symlinks and ignores junk. It returns
// one group per first-level folder plus one group for loose files, sorted by
// hint; empty groups are left out. A missing romsDir gives no groups. More
// than max files give ErrTooManyFiles.
func Scan(romsDir string, max int) ([]Group, []Skip, error) {
	if ok, err := isDir(romsDir); err != nil || !ok {
		return nil, nil, err
	}
	byHint := map[string]*Group{}
	var skips []Skip
	count := 0
	err := filepath.WalkDir(romsDir, func(p string, d fs.DirEntry, werr error) error {
		if p == romsDir {
			return werr
		}
		relOS, err := filepath.Rel(romsDir, p)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(relOS)
		name := d.Name()
		if isJunk(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		skip := func(reason string) error {
			skips = append(skips, Skip{Rel: rel, Reason: reason})
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case werr != nil:
			return skip(reasonReadError)
		case d.Type()&fs.ModeSymlink != 0:
			return skip(reasonSymlink)
		case !safeName(name):
			return skip(reasonUnsafe)
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return skip(reasonNotFile)
		}
		info, err := d.Info()
		if err != nil {
			return skip(reasonReadError)
		}
		count++
		if count > max {
			return ErrTooManyFiles
		}
		hint, sub, ok := strings.Cut(rel, "/")
		if !ok {
			hint, sub = "", rel
		}
		g := byHint[hint]
		if g == nil {
			g = &Group{Hint: hint, Dir: filepath.Join(romsDir, filepath.FromSlash(hint))}
			byHint[hint] = g
		}
		g.Rels = append(g.Rels, sub)
		g.Bytes += info.Size()
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	groups := make([]Group, 0, len(byHint))
	for _, g := range byHint {
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Hint < groups[j].Hint })
	return groups, skips, nil
}

// NeedBytes returns the space that staging the groups needs, with the
// decisions StageTree makes (roms.Stager.PlanTree):
//   - a file that is not copied (already imported, a damaged ZIP, a ZIP that
//     is over the unpack limits or cannot be read, a ZIP whose files are all
//     imported): 0;
//   - a ZIP that is unpacked: its unpacked size. While one ZIP is unpacked,
//     its copy (<id>.part) and its content exist together, so the size of the
//     largest such ZIP is added once as the peak;
//   - every other file, also a kept ZIP (for example arcade): its size.
//
// Then 5 % reserve is added. A sum that does not fit into an int64 is
// math.MaxInt64, which is never enough space.
func NeedBytes(st *roms.Stager, groups []Group) int64 {
	return needBytes(planGroups(st, groups))
}

// planGroups plans every group (roms.Stager.PlanTree), in the order of groups.
func planGroups(st *roms.Stager, groups []Group) [][]roms.TreeFile {
	plans := make([][]roms.TreeFile, len(groups))
	for i, g := range groups {
		plans[i] = st.PlanTree(g.Dir, g.Hint, g.Rels)
	}
	return plans
}

func needBytes(plans [][]roms.TreeFile) int64 {
	var total, peak int64
	for _, ps := range plans {
		for _, p := range ps {
			switch {
			case p.Reason != "":
			case p.Unpack:
				total = addSat(total, p.Unpacked)
				peak = max(peak, p.Size)
			default:
				total = addSat(total, p.Size)
			}
		}
	}
	total = addSat(total, peak)
	return addSat(total, total/20)
}

// addSat adds two sizes (not negative); a sum over math.MaxInt64 is
// math.MaxInt64.
func addSat(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// freeBytes returns the free space for unprivileged users on the file system
// that holds path. Tests replace it.
var freeBytes = func(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

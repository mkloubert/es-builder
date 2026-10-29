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
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Report statuses.
const (
	StatusStaged        = "staged"
	StatusReview        = "needs review"
	StatusBIOSSaved     = "bios saved"
	StatusBIOSImported  = "bios skipped: already imported"
	StatusBIOSOverwrite = "bios overwritten"
	statusSkippedPrefix = "skipped: "
	reportName          = "import-report.txt"
)

// Line is one line of the import report.
type Line struct {
	Rel    string // path on the stick, relative to the stick root
	Status string
	Note   string // optional, for example "folder snes does not accept .iso"
}

func skipped(reason string) string { return statusSkippedPrefix + reason }

var statusOrder = map[string]int{
	StatusStaged: 1, StatusReview: 2, StatusBIOSSaved: 3, StatusBIOSOverwrite: 4, StatusBIOSImported: 5,
}

// WriteReport writes (overwrites) <base>/import-report.txt: a header with the
// date and the count per status, then one line per file. It does not follow
// symlinks: a link named import-report.txt is an error and stays as it is.
func WriteReport(base string, now time.Time, lines []Line) error {
	counts := map[string]int{}
	for _, l := range lines {
		counts[l.Status]++
	}
	statuses := make([]string, 0, len(counts))
	for s := range counts {
		statuses = append(statuses, s)
	}
	sort.Slice(statuses, func(i, j int) bool {
		a, b := statuses[i], statuses[j]
		oa, ob := statusOrder[a], statusOrder[b]
		if oa == 0 {
			oa = 100
		}
		if ob == 0 {
			ob = 100
		}
		if oa != ob {
			return oa < ob
		}
		return a < b
	})

	var b strings.Builder
	b.WriteString("EmuStation Builder - import report\n")
	fmt.Fprintf(&b, "Date: %s\n", now.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&b, "Files: %d\n", len(lines))
	for _, s := range statuses {
		fmt.Fprintf(&b, "%s: %d\n", s, counts[s])
	}
	b.WriteString("\n")
	for _, l := range lines {
		b.WriteString(l.Rel + " - " + l.Status)
		if l.Note != "" {
			b.WriteString(" (" + l.Note + ")")
		}
		b.WriteString("\n")
	}
	if ok, err := realDir(base, base); err != nil || !ok {
		return linkError(filepath.Base(base))
	}
	return writeFileNoFollow(filepath.Dir(base), base, reportName, []byte(b.String()))
}

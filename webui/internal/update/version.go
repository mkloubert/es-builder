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

// Package update holds the pure helpers of the web UI self-update:
// tags, versions and update URLs.
package update

import (
	"regexp"
	"strconv"
	"strings"
)

// Version is a web UI version x.y.z.
type Version struct{ Major, Minor, Patch int }

var verRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)

const tagPrefix = "webui-v"

// String returns the version as x.y.z.
func (v Version) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}

// Less reports whether v is older than o. Each part is compared as a number.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// ParseVersion parses "x.y.z". Leading zeros and extra text are refused.
func ParseVersion(s string) (Version, bool) {
	m := verRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	// The regex limits each part to 6 digits, so Atoi cannot fail.
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return Version{maj, min, pat}, true
}

// ParseTag parses a tag name like "webui-v1.2.3".
func ParseTag(name string) (Version, bool) {
	rest, ok := strings.CutPrefix(name, tagPrefix)
	if !ok {
		return Version{}, false
	}
	return ParseVersion(rest)
}

// LatestTag returns the highest version in the output of "git ls-remote --tags".
// Lines look like "<sha>\trefs/tags/<name>" or "...<name>^{}". Other lines are skipped.
func LatestTag(lsRemote string) (Version, bool) {
	var best Version
	found := false
	for _, line := range strings.Split(lsRemote, "\n") {
		line = strings.TrimSuffix(line, "\r")
		_, ref, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		name, ok := strings.CutPrefix(ref, "refs/tags/")
		if !ok {
			continue
		}
		name = strings.TrimSuffix(name, "^{}")
		v, ok := ParseTag(name)
		if !ok {
			continue
		}
		if !found || best.Less(v) {
			best, found = v, true
		}
	}
	return best, found
}

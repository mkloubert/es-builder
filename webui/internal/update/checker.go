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

package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is the result of the last check. Err is a short code, never raw git text.
type State struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
	Err       string    `json:"error"`
}

// The error codes of State.Err.
const (
	ErrCodeUnreachable = "unreachable"
	ErrCodeAuth        = "auth"
	ErrCodeNoTags      = "no-tags"
	ErrCodeBadURL      = "bad-url"
)

// Checker looks for new versions and keeps the last result in File.
// Zero First means 2 minutes. Zero Every means 4 hours.
type Checker struct {
	Git          Git
	File         string
	Current      Version
	Now          func() time.Time
	First, Every time.Duration
}

// authHints are parts of git stderr that point to a login problem.
var authHints = []string{
	"Permission denied",
	"Authentication failed",
	"could not read Username",
	"Host key verification failed",
}

// errCode maps a git error to a short code.
func errCode(err error) string {
	msg := err.Error()
	for _, h := range authHints {
		if strings.Contains(msg, h) {
			return ErrCodeAuth
		}
	}
	return ErrCodeUnreachable
}

// Check asks the remote for tags, saves the result and returns it.
func (c *Checker) Check(ctx context.Context) State {
	st := State{CheckedAt: c.now()}
	st.Latest = c.Load().Latest
	st.Err = c.look(ctx, &st)
	c.save(st)
	return st
}

// look fills st.Latest and returns an error code or "".
func (c *Checker) look(ctx context.Context, st *State) string {
	k, err := CheckURL(c.Git.S.URL)
	if err != nil || checkPaths(c.Git.S) != nil {
		return ErrCodeBadURL
	}
	if k == KindLocal {
		if fi, err := os.Stat(LocalPath(c.Git.S.URL)); err != nil || !fi.IsDir() {
			return ErrCodeUnreachable
		}
	}
	out, err := c.Git.LsRemote(ctx)
	if err != nil {
		return errCode(err)
	}
	v, ok := LatestTag(out)
	if !ok {
		return ErrCodeNoTags
	}
	st.Latest = v.String()
	return ""
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// save writes the state with a temp file and a rename. Errors are ignored:
// the result is still returned to the caller.
func (c *Checker) save(st State) {
	if c.File == "" {
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	dir := filepath.Dir(c.File)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	f, err := os.CreateTemp(dir, ".check-*.tmp")
	if err != nil {
		return
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil || cerr != nil || os.Rename(f.Name(), c.File) != nil {
		os.Remove(f.Name())
	}
}

// Load returns the saved state, or an empty state if there is none.
func (c *Checker) Load() State {
	var st State
	b, err := os.ReadFile(c.File)
	if err != nil || json.Unmarshal(b, &st) != nil {
		return State{}
	}
	return st
}

// Run checks 2 minutes after the start, then every 4 hours. It stops when ctx ends.
func (c *Checker) Run(ctx context.Context) {
	first, every := c.First, c.Every
	if first <= 0 {
		first = 2 * time.Minute
	}
	if every <= 0 {
		every = 4 * time.Hour
	}
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			c.Check(ctx)
			timer.Reset(every)
		}
	}
}

// Newer ignores Err: the UI shows the error on its own.
// Newer returns the saved latest version if it is strictly higher than cur.
func (s State) Newer(cur Version) (Version, bool) {
	v, ok := ParseVersion(s.Latest)
	if !ok || !cur.Less(v) {
		return Version{}, false
	}
	return v, true
}

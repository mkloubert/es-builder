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
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Kind tells how git reaches the update source.
type Kind int

// The kinds of update URLs.
const (
	KindHTTPS Kind = iota + 1
	KindSSH
	KindLocal
)

const maxURLLen = 512

var (
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
	userRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	portRe    = regexp.MustCompile(`^[0-9]{1,5}$`)
	urlPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~/+@=,:-]*$`)

	errURL = errors.New("update url not allowed")
)

// CheckURL checks an update URL and returns its kind. The errors are for logs only.
func CheckURL(s string) (Kind, error) {
	if s == "" || len(s) > maxURLLen || s[0] == '-' {
		return 0, errURL
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c <= ' ' || c == 0x7f || c == '\\' {
			return 0, errURL
		}
	}
	switch {
	case strings.HasPrefix(s, "https://"):
		return checkNet(s, "https", KindHTTPS)
	case strings.HasPrefix(s, "ssh://"):
		return checkNet(s, "ssh", KindSSH)
	case strings.HasPrefix(s, "file://"):
		if strings.Contains(s, "%") || cleanLocal(strings.TrimPrefix(s, "file://")) == "" {
			return 0, errURL
		}
		return KindLocal, nil
	case strings.HasPrefix(s, "/"):
		if cleanLocal(s) == "" {
			return 0, errURL
		}
		return KindLocal, nil
	case strings.Contains(s, "://"):
		return 0, errURL
	}
	return checkSCP(s)
}

// checkNet checks an https:// or ssh:// URL.
func checkNet(s, scheme string, kind Kind) (Kind, error) {
	if strings.ContainsAny(s, "?#%") {
		return 0, errURL
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != scheme || u.Opaque != "" {
		return 0, errURL
	}
	if !hostRe.MatchString(u.Hostname()) || u.Host != hostPort(u) {
		return 0, errURL
	}
	if p := u.Port(); p != "" && !portRe.MatchString(p) {
		return 0, errURL
	}
	if u.User != nil {
		if kind == KindHTTPS {
			return 0, errURL
		}
		if _, hasPw := u.User.Password(); hasPw || !userRe.MatchString(u.User.Username()) {
			return 0, errURL
		}
	}
	if u.Path == "" || u.Path == "/" || u.RawPath != "" {
		return 0, errURL
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." {
			return 0, errURL
		}
	}
	if !urlPathRe.MatchString(u.Path) {
		return 0, errURL
	}
	return kind, nil
}

// hostPort rebuilds host[:port] from the parsed parts. It differs from u.Host
// for odd input such as brackets or an empty port.
func hostPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return u.Hostname() + ":" + p
	}
	return u.Hostname()
}

// checkSCP checks the scp form user@host:path.
func checkSCP(s string) (Kind, error) {
	user, rest, ok := strings.Cut(s, "@")
	if !ok || !userRe.MatchString(user) {
		return 0, errURL
	}
	host, p, ok := strings.Cut(rest, ":")
	if !ok || !hostRe.MatchString(host) {
		return 0, errURL
	}
	if p == "" || p[0] == '-' || p[0] == '/' {
		return 0, errURL
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return 0, errURL
		}
	}
	return KindSSH, nil
}

// cleanLocal returns the absolute folder without a trailing slash,
// or "" if p is not absolute and clean.
func cleanLocal(p string) string {
	p = strings.TrimSuffix(p, "/")
	if !strings.HasPrefix(p, "/") || p == "/" || path.Clean(p) != p {
		return ""
	}
	return p
}

// LocalPath returns the folder of a file:// or absolute path URL.
// For any other or bad URL it returns "".
func LocalPath(s string) string {
	if k, err := CheckURL(s); err != nil || k != KindLocal {
		return ""
	}
	return cleanLocal(strings.TrimPrefix(s, "file://"))
}

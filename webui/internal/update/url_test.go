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
	"strings"
	"testing"
)

func TestCheckURL(t *testing.T) {
	tests := []struct {
		in   string
		kind Kind
		ok   bool
	}{
		{"https://github.com/mkloubert/es-builder", KindHTTPS, true},
		{"https://git.example:8443/a/b.git", KindHTTPS, true},
		{"ssh://git@host:2222/a/b.git", KindSSH, true},
		{"ssh://host/a/b.git", KindSSH, true},
		{"git@github.com:a/b.git", KindSSH, true},
		{"file:///mnt/repo", KindLocal, true},
		{"/media/usb/es-builder", KindLocal, true},
		{"/media/usb/es-builder/", KindLocal, true},

		{"", 0, false},
		{"http://h/x", 0, false},
		{"HTTPS://h/x", 0, false},
		{"git://h/x", 0, false},
		{"ext::sh -c x", 0, false},
		{"ext::x", 0, false},
		{"-oProxyCommand=x", 0, false},
		{"--upload-pack=x", 0, false},
		{"https://u:p@h/x", 0, false},
		{"https://u@h/x", 0, false},
		{"https://h/x?y", 0, false},
		{"https://h/x#y", 0, false},
		{"https://h", 0, false},
		{"https://h:port/x", 0, false},
		{"https://h_x/x", 0, false},
		{"ssh://git:pw@host/a", 0, false},
		{"ssh://git@host/a?x", 0, false},
		{"ssh://git@host/a#x", 0, false},
		{"ssh://-oProxyCommand=x@host/a", 0, false},
		{"ssh://host", 0, false},
		{"ssh://-oProxyCommand/a", 0, false},
		{"ssh://-oX@host/a", 0, false},
		{"git@-oX:a", 0, false},
		{"https://-h/x", 0, false},
		{"/mnt/../etc", 0, false},
		{"/mnt/./x", 0, false},
		{"/mnt//x", 0, false},
		{"/", 0, false},
		{"relative/path", 0, false},
		{"//h/x", 0, false},
		{"file://h/x", 0, false},
		{"file:///mnt/../x", 0, false},
		{"file:///mnt/%2e%2e/x", 0, false},
		{"file://relative", 0, false},
		{"file:////x", 0, false},
		{"git@h:../x", 0, false},
		{"git@h:a/../x", 0, false},
		{"git@h:", 0, false},
		{"git@h:/abs", 0, false},
		{"git@h:-x", 0, false},
		{"-u@h:x", 0, false},
		{"@h:x", 0, false},
		{"git@:x", 0, false},
		{"a b@h:x", 0, false},
		{"https://h/a b", 0, false},
		{"https://h/a\nb", 0, false},
		{"https://h/a\tb", 0, false},
		{"https://h/a\x7fb", 0, false},
		{"https://h/a\\b", 0, false},
		{"\n", 0, false},
		{"https://github.com/o/repo(1)", 0, false},
		{"https://h/päth", 0, false},
		{"https://h/a!b", 0, false},
		{"https://h/a*b", 0, false},
		{"https://h/x'y", 0, false},
		{"https://h/a[b]", 0, false},
		{"https://h/a%20b", 0, false},
		{"ssh://us%41er@h/x", 0, false},
		{"https://h/" + strings.Repeat("a", 512), 0, false},
	}
	for _, tc := range tests {
		k, err := CheckURL(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("CheckURL(%q) err = %v; want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && k != tc.kind {
			t.Errorf("CheckURL(%q) kind = %v; want %v", tc.in, k, tc.kind)
		}
	}
}

func TestCheckURLLength(t *testing.T) {
	base := "https://h/"
	if _, err := CheckURL(base + strings.Repeat("a", 512-len(base))); err != nil {
		t.Errorf("512 bytes should pass: %v", err)
	}
	if _, err := CheckURL(base + strings.Repeat("a", 513-len(base))); err == nil {
		t.Error("513 bytes should fail")
	}
}

func TestLocalPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"file:///mnt/repo", "/mnt/repo"},
		{"/media/usb/es-builder", "/media/usb/es-builder"},
		{"/media/usb/es-builder/", "/media/usb/es-builder"},
		{"https://github.com/a/b", ""},
		{"ssh://git@h/a", ""},
		{"git@h:a/b", ""},
		{"/mnt/../etc", ""},
		{"relative", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := LocalPath(tc.in); got != tc.want {
			t.Errorf("LocalPath(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

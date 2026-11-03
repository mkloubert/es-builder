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

import "testing"

func TestParseTag(t *testing.T) {
	tests := []struct {
		in   string
		want Version
		ok   bool
	}{
		{"webui-v0.1.2", Version{0, 1, 2}, true},
		{"webui-v1.10.0", Version{1, 10, 0}, true},
		{"webui-v999999.0.0", Version{999999, 0, 0}, true},
		{"webui-v01.2.3", Version{}, false},
		{"webui-v1.2", Version{}, false},
		{"webui-v1.2.3-rc1", Version{}, false},
		{"webui-v1.2.3 ", Version{}, false},
		{"webui-v1.2.3\n", Version{}, false},
		{"xwebui-v1.2.3", Version{}, false},
		{"webui-v1234567.0.0", Version{}, false},
		{"", Version{}, false},
	}
	for _, tc := range tests {
		got, ok := ParseTag(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseTag(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if v, ok := ParseVersion("1.2.3"); !ok || v.String() != "1.2.3" {
		t.Errorf("got %v, %v", v, ok)
	}
	for _, s := range []string{"", "1.2", "01.2.3", "v1.2.3", "1.2.3\n", "1.2.3-rc1", "1.2.3.4"} {
		if _, ok := ParseVersion(s); ok {
			t.Errorf("ParseVersion(%q) should fail", s)
		}
	}
}

func TestLess(t *testing.T) {
	tests := []struct {
		a, b Version
		want bool
	}{
		{Version{1, 9, 9}, Version{1, 10, 0}, true},
		{Version{1, 10, 0}, Version{1, 9, 9}, false},
		{Version{1, 2, 3}, Version{1, 2, 3}, false},
		{Version{0, 9, 9}, Version{1, 0, 0}, true},
		{Version{1, 2, 3}, Version{1, 2, 4}, true},
	}
	for _, tc := range tests {
		if got := tc.a.Less(tc.b); got != tc.want {
			t.Errorf("%v.Less(%v) = %v", tc.a, tc.b, got)
		}
	}
}

func TestLatestTag(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	mixed := sha + "\trefs/heads/main\n" +
		sha + "\trefs/tags/webui-v1.9.9\n" +
		sha + "\trefs/tags/webui-v1.9.9^{}\n" +
		sha + "\trefs/tags/webui-v1.10.0\n" +
		sha + "\trefs/tags/webui-v1.10.0^{}\n" +
		sha + "\trefs/tags/v9.9.9\n" +
		sha + "\trefs/tags/webui-v2.0.0-rc1\n" +
		sha + "\trefs/heads/webui-v5.0.0\n" +
		"garbage line\n"
	tests := []struct {
		name string
		in   string
		want Version
		ok   bool
	}{
		{"mixed", mixed, Version{1, 10, 0}, true},
		{"only peeled", sha + "\trefs/tags/webui-v3.0.0^{}\n", Version{3, 0, 0}, true},
		{"crlf", sha + "\trefs/tags/webui-v1.0.0\r\n", Version{1, 0, 0}, true},
		{"none", sha + "\trefs/heads/main\n", Version{}, false},
		{"empty", "", Version{}, false},
	}
	for _, tc := range tests {
		got, ok := LatestTag(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got %v, %v; want %v, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

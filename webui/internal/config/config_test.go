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

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "0.0.0.0:11944" {
		t.Errorf("Listen = %q", c.Listen)
	}
	if c.MaxZipEntries != 10000 {
		t.Errorf("MaxZipEntries = %d", c.MaxZipEntries)
	}
	if c.Paths.Roms != "/home/emulationstation/RetroPie/roms" {
		t.Errorf("Roms = %q", c.Paths.Roms)
	}
	if c.SourceURL != "https://github.com/mkloubert/es-builder" {
		t.Errorf("SourceURL = %q", c.SourceURL)
	}
}

func TestSourceURLEmptyAllowedAndRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "web.conf")
	if err := os.WriteFile(p, []byte("SOURCE_URL=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.SourceURL != "" {
		t.Errorf("SourceURL = %q, want empty", c.SourceURL)
	}
	c.SourceURL = "https://example.org/src"
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceURL != "https://example.org/src" {
		t.Errorf("SourceURL = %q", got.SourceURL)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "web.conf")
	c := Default()
	c.WebUser = "alice"
	c.WebPasswordHash = "hash"
	c.ScreenscraperUser = "ss"
	c.ScreenscraperPass = "p=w"
	c.Listen = "127.0.0.1:1"
	c.TLSCert = "/a"
	c.TLSKey = "/b"
	c.MaxUploadBytes = 5 << 20
	c.MaxExtractBytes = 7 << 20
	c.MaxZipEntries = 12
	c.setHome("/home/x")
	c.Paths.ESSystemsCfg = "/tmp/sys.cfg"
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Errorf("mismatch:\n got %+v\nwant %+v", got, c)
	}
}

func TestSaveRejectsNewline(t *testing.T) {
	c := Default()
	c.WebUser = "a\nb"
	if err := c.Save(filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadIgnoresUnknownAndComments(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c")
	os.WriteFile(p, []byte("# hi\nFOO=bar\n\nWEB_USER=bob\nLISTEN=:1\n"), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.WebUser != "bob" || c.Listen != ":1" {
		t.Errorf("%+v", c)
	}
}

func TestCheckCredentials(t *testing.T) {
	h, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.WebUser, c.WebPasswordHash = "u", h
	if !c.CheckCredentials("u", "pw") {
		t.Error("correct should pass")
	}
	if c.CheckCredentials("u", "bad") {
		t.Error("bad pw")
	}
	if c.CheckCredentials("x", "pw") {
		t.Error("bad user")
	}
	c.WebPasswordHash = ""
	if c.CheckCredentials("u", "pw") {
		t.Error("empty hash")
	}
}

func TestPasswordWithColonAndUmlaut(t *testing.T) {
	h, err := HashPassword("a:b:ü")
	if err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.WebUser, c.WebPasswordHash = "u", h
	if !c.CheckCredentials("u", "a:b:ü") {
		t.Error("should pass")
	}
}

func TestSaveOverExistingKeepsModeAndOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web.conf")
	c := Default()
	c.WebUser = "a"
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	c.WebUser = "b"
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	if got, _ := Load(path); got.WebUser != "b" {
		t.Fatal(got.WebUser)
	}
}

func TestSaveChownsToExistingOwnerAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	path := filepath.Join(t.TempDir(), "web.conf")
	if err := Default().Save(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, 1234, 4321); err != nil {
		t.Fatal(err)
	}
	if err := Default().Save(path); err != nil {
		t.Fatal(err)
	}
	st := mustStat(t, path)
	if st.Uid != 1234 || st.Gid != 4321 {
		t.Fatalf("owner %d:%d", st.Uid, st.Gid)
	}
}

func mustStat(t *testing.T, p string) *syscall.Stat_t {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t)
}

func TestUSBMaxFilesDefaultLoadSave(t *testing.T) {
	if got := Default().USBMaxFiles; got != 20000 {
		t.Fatalf("default USBMaxFiles = %d", got)
	}
	p := filepath.Join(t.TempDir(), "web.conf")
	if err := os.WriteFile(p, []byte("USB_MAX_FILES=42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.USBMaxFiles != 42 {
		t.Fatal(c.USBMaxFiles, err)
	}
	c.USBMaxFiles = 7
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "USB_MAX_FILES=7\n") {
		t.Fatalf("saved file:\n%s", data)
	}
	got, err := Load(p)
	if err != nil || got.USBMaxFiles != 7 {
		t.Fatal(got.USBMaxFiles, err)
	}
}

func TestUSBMaxFilesRejectsZeroAndGarbage(t *testing.T) {
	for _, v := range []string{"0", "-1", "abc"} {
		p := filepath.Join(t.TempDir(), "web.conf")
		os.WriteFile(p, []byte("USB_MAX_FILES="+v+"\n"), 0o600)
		if _, err := Load(p); err == nil {
			t.Errorf("%q: want error", v)
		}
	}
}

func TestUIDir(t *testing.T) {
	cases := []struct{ name, conf, want string }{
		{"default", "", "/home/emulationstation/webui-custom"},
		{"from home", "HOME_DIR=/h\n", "/h/webui-custom"},
		{"empty is off", "UI_DIR=\n", ""},
		{"own path", "HOME_DIR=/h\nUI_DIR=/x\n", "/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "web.conf")
			if err := os.WriteFile(p, []byte(tc.conf), 0o600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(p)
			if err != nil {
				t.Fatal(err)
			}
			if c.UIDir != tc.want {
				t.Fatalf("UIDir = %q, want %q", c.UIDir, tc.want)
			}
			if err := c.Save(p); err != nil {
				t.Fatal(err)
			}
			got, err := Load(p)
			if err != nil {
				t.Fatal(err)
			}
			if got.UIDir != tc.want {
				t.Fatalf("after round trip UIDir = %q, want %q", got.UIDir, tc.want)
			}
		})
	}
}

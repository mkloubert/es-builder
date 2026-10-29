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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"emulationstation/webui/internal/config"
)

func TestCredentialsCommandSetsUserAndHash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "web.conf")
	var out, errb bytes.Buffer
	code := run([]string{"credentials", "--config", p, "--user", "newuser", "--password-stdin"}, strings.NewReader("secret\n"), &out, &errb)
	if code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.CheckCredentials("newuser", "secret") {
		t.Error("credentials not set")
	}
	// without --user the user stays
	code = run([]string{"credentials", "--config", p, "--password-stdin"}, strings.NewReader("second\n"), &out, &errb)
	if code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	c, _ = config.Load(p)
	if !c.CheckCredentials("newuser", "second") {
		t.Error("user should remain neu")
	}
}

func TestCredentialsNeedsSomething(t *testing.T) {
	p := filepath.Join(t.TempDir(), "web.conf")
	var out, errb bytes.Buffer
	if run([]string{"credentials", "--config", p}, strings.NewReader(""), &out, &errb) == 0 {
		t.Error("expected failure")
	}
}

func TestPasswd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "web.conf")
	var out, errb bytes.Buffer
	code := run([]string{"passwd", "--config", p}, strings.NewReader("me\npw1\npw1\n"), &out, &errb)
	if code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	c, _ := config.Load(p)
	if !c.CheckCredentials("me", "pw1") {
		t.Error("not set")
	}
	code = run([]string{"passwd", "--config", p}, strings.NewReader("me\na\nb\n"), &out, &errb)
	if code == 0 {
		t.Error("mismatch should fail")
	}
}

func TestServeFailsWithoutConfigAndUnknown(t *testing.T) {
	var out, errb bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.conf")
	if run([]string{"serve", "--config", missing}, strings.NewReader(""), &out, &errb) != 1 || !strings.Contains(errb.String(), "serve:") {
		t.Error("serve with unreadable config should fail")
	}
	if run([]string{"bogus"}, strings.NewReader(""), &out, &errb) == 0 {
		t.Error("unknown should fail")
	}
}

func TestGUIDCommand(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"guid", "--bus", "3", "--vendor", "0x045e", "--product", "0x028e", "--version", "0x0114", "--name", "Microsoft X-Box 360 pad"}
	if code := run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if out.String() != "030000005e0400008e02000014010000\n" {
		t.Fatalf("got %q", out.String())
	}
	out.Reset()
	run(append(args, "--crc"), strings.NewReader(""), &out, &errb)
	if out.String() == "030000005e0400008e02000014010000\n" {
		t.Fatal("crc not applied")
	}
	errb.Reset()
	if code := run([]string{"guid", "--vendor", "zz"}, strings.NewReader(""), &out, &errb); code != 2 || errb.Len() == 0 {
		t.Fatalf("code %d %q", code, errb.String())
	}
}

func TestCfgHolderReloadsExternalChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web.conf")
	c := config.Default()
	c.WebUser, c.WebPasswordHash = "u", "hash1"
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, _ := config.Load(path)
	var logw bytes.Buffer
	h := newCfgHolder(path, loaded, &logw)
	if h.get().WebPasswordHash != "hash1" {
		t.Fatal("initial")
	}

	ext, _ := config.Load(path)
	ext.WebPasswordHash = "hash-external-2"
	if err := ext.Save(path); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	os.Chtimes(path, future, future)
	if got := h.get().WebPasswordHash; got != "hash-external-2" {
		t.Fatalf("get() = %q", got)
	}

	ext.WebPasswordHash = "hash-external-3"
	ext.Save(path)
	os.Chtimes(path, future.Add(time.Hour), future.Add(time.Hour))
	err := h.update(func(c *config.Config) error { c.ScreenscraperUser = "ss"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	final, _ := config.Load(path)
	if final.WebPasswordHash != "hash-external-3" || final.ScreenscraperUser != "ss" {
		t.Fatalf("update overwrote external change: %+v", final)
	}

	os.WriteFile(path, []byte("MAX_UPLOAD_MB=abc\n"), 0o600)
	os.Chtimes(path, future.Add(2*time.Hour), future.Add(2*time.Hour))
	if h.get().WebPasswordHash != "hash-external-3" {
		t.Fatal("broken file must keep current config")
	}
	h.get()
	if n := strings.Count(logw.String(), "config reload"); n != 1 {
		t.Fatalf("logged %d times: %q", n, logw.String())
	}
}

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
	"os"
	"strings"
	"testing"
)

func TestParsePinsReal(t *testing.T) {
	df, err := os.ReadFile("testdata/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		arch string
		want Pins
	}{
		{"arm64", Pins{"1.26.10", "3f4227ce7b206a513e794f1393adb473a07b46e94d18bd15d1c684bbb7319797", "v4.3.3", "55fd0b241214eff3de1e8ee4f22796662f2d2e7a49bcfca7477cfd0bac398195"}},
		{"amd64", Pins{"1.26.10", "ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5", "v4.3.3", "dc61b3ac6b8c9ca874c0cc4c57b2409791a64c5540404ca5f5367360babc313a"}},
	}
	for _, c := range cases {
		got, err := ParsePins(df, c.arch)
		if err != nil || got != c.want {
			t.Errorf("%s: got %+v, %v", c.arch, got, err)
		}
	}
}

func TestParsePinsErrors(t *testing.T) {
	sha := strings.Repeat("a", 64)
	good := "ARG GO_VERSION=1.26.10\nARG GO_SHA256_AMD64=" + sha + "\nARG TAILWIND_VERSION=v4.3.3\nARG TAILWIND_SHA256_X64=" + sha + "\n"
	if _, err := ParsePins([]byte(good), "amd64"); err != nil {
		t.Fatalf("good: %v", err)
	}
	bad := map[string]string{
		"missing go sha": strings.Replace(good, "GO_SHA256_AMD64", "GO_SHA256_OTHER", 1),
		"63 chars":       strings.Replace(good, "GO_SHA256_AMD64="+sha, "GO_SHA256_AMD64="+sha[:63], 1),
		"uppercase":      strings.Replace(good, "GO_SHA256_AMD64="+sha, "GO_SHA256_AMD64="+strings.Repeat("A", 64), 1),
		"trailing text":  strings.Replace(good, "GO_VERSION=1.26.10", "GO_VERSION=1.26.10 # x", 1),
		"dup go version": good + "ARG GO_VERSION=1.26.11\n",
		"dup tw sha":     good + "ARG TAILWIND_SHA256_X64=" + sha + "\n",
		"bad go version": strings.Replace(good, "1.26.10", "1.26", 1),
		"bad tw version": strings.Replace(good, "v4.3.3", "4.3.3", 1),
	}
	for name, df := range bad {
		if _, err := ParsePins([]byte(df), "amd64"); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := ParsePins([]byte(good), "riscv64"); err == nil {
		t.Error("unknown arch: want error")
	}
}

func TestToolURLs(t *testing.T) {
	p := Pins{Go: "1.26.10", Tailwind: "v4.3.3"}
	g, w := ToolURLs(p, "amd64")
	if g != "https://go.dev/dl/go1.26.10.linux-amd64.tar.gz" ||
		w != "https://github.com/tailwindlabs/tailwindcss/releases/download/v4.3.3/tailwindcss-linux-x64" {
		t.Errorf("amd64: %s %s", g, w)
	}
	g, w = ToolURLs(p, "arm64")
	if !strings.HasSuffix(g, "linux-arm64.tar.gz") || !strings.HasSuffix(w, "tailwindcss-linux-arm64") {
		t.Errorf("arm64: %s %s", g, w)
	}
}

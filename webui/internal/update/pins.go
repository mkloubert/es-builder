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
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
)

// Pins are the tool versions and checksums from builder/Dockerfile.
type Pins struct{ Go, GoSHA, Tailwind, TailwindSHA string }

var (
	argRe    = regexp.MustCompile(`^ARG ([A-Z0-9_]+)=(\S+)$`)
	goVerRe  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	twVerRe  = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// twArch maps a GOARCH to the Tailwind release suffix.
func twArch(goarch string) (string, bool) {
	switch goarch {
	case "amd64":
		return "x64", true
	case "arm64":
		return "arm64", true
	}
	return "", false
}

// ParsePins reads the pins for goarch from the Dockerfile. Only lines like
// "ARG NAME=value" count. Other lines are ignored.
func ParsePins(dockerfile []byte, goarch string) (Pins, error) {
	tw, ok := twArch(goarch)
	if !ok {
		return Pins{}, fmt.Errorf("unsupported arch %q", goarch)
	}
	args := map[string]string{}
	seen := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(dockerfile))
	for sc.Scan() {
		if m := argRe.FindStringSubmatch(sc.Text()); m != nil {
			args[m[1]] = m[2]
			seen[m[1]]++
		}
	}
	upper := map[string]string{"amd64": "AMD64", "arm64": "ARM64"}[goarch]
	for _, k := range []string{"GO_VERSION", "GO_SHA256_" + upper, "TAILWIND_VERSION", "TAILWIND_SHA256_" + map[string]string{"x64": "X64", "arm64": "ARM64"}[tw]} {
		if seen[k] > 1 {
			return Pins{}, errors.New("pin set twice: " + k)
		}
	}
	p := Pins{
		Go:          args["GO_VERSION"],
		GoSHA:       args["GO_SHA256_"+upper],
		Tailwind:    args["TAILWIND_VERSION"],
		TailwindSHA: args["TAILWIND_SHA256_"+map[string]string{"x64": "X64", "arm64": "ARM64"}[tw]],
	}
	switch {
	case !goVerRe.MatchString(p.Go):
		return Pins{}, errors.New("bad or missing GO_VERSION")
	case !sha256Re.MatchString(p.GoSHA):
		return Pins{}, errors.New("bad or missing Go checksum")
	case !twVerRe.MatchString(p.Tailwind):
		return Pins{}, errors.New("bad or missing TAILWIND_VERSION")
	case !sha256Re.MatchString(p.TailwindSHA):
		return Pins{}, errors.New("bad or missing Tailwind checksum")
	}
	return p, nil
}

// ToolURLs returns the fixed download URLs of Go and Tailwind for goarch.
func ToolURLs(p Pins, goarch string) (goURL, twURL string) {
	tw, _ := twArch(goarch)
	return "https://go.dev/dl/go" + p.Go + ".linux-" + goarch + ".tar.gz",
		"https://github.com/tailwindlabs/tailwindcss/releases/download/" + p.Tailwind + "/tailwindcss-linux-" + tw
}

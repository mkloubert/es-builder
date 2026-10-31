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

// Package legal holds the license texts and the list of third-party
// components that are shipped with the web UI.
package legal

import (
	"embed"
	"runtime"
	"runtime/debug"
)

//go:embed LICENSE THIRD-PARTY-NOTICES licenses
var fsys embed.FS

// Component is one third-party part of the web UI.
// Files are paths relative to the web static folder that the component
// covers. They are empty for code that is compiled into the binary.
type Component struct {
	Name, Version, SPDX, URL string
	Files                    []string
	LicenseFile              string
}

// AllowedSPDX lists the license ids that third-party parts may use.
var AllowedSPDX = map[string]bool{
	"MIT":               true,
	"BSD-2-Clause":      true,
	"BSD-3-Clause":      true,
	"ISC":               true,
	"0BSD":              true,
	"Apache-2.0":        true,
	"OFL-1.1":           true,
	"GPL-3.0-or-later":  true,
	"AGPL-3.0-or-later": true,
}

// Components lists all third-party parts, in the order of the spec.
// Files are relative to the static folder. app.css is generated at build time.
// One file can be covered by more than one component, for example a bundle.
var Components = []Component{
	{Name: "htmx", Files: []string{"vendor/htmx.min.js"}, Version: "2.0.11", SPDX: "0BSD", URL: "https://htmx.org", LicenseFile: "htmx.txt"},
	{Name: "Alpine.js", Files: []string{"vendor/alpine.min.js"}, Version: "3.17.5", SPDX: "MIT", URL: "https://alpinejs.dev", LicenseFile: "alpinejs.txt"},
	{Name: "Vue reactivity (bundled in Alpine.js)", Files: []string{"vendor/alpine.min.js"}, Version: "3.5.41", SPDX: "MIT", URL: "https://github.com/vuejs/core", LicenseFile: "vue.txt"},
	{Name: "Tailwind CSS", Files: []string{"app.css"}, Version: "4.3.3", SPDX: "MIT", URL: "https://tailwindcss.com", LicenseFile: "tailwindcss.txt"},
	{Name: "Lucide", Files: []string{"icons.svg"}, Version: "1.66.0", SPDX: "ISC", URL: "https://lucide.dev", LicenseFile: "lucide.txt"},
	{Name: "Bebas Neue", Files: []string{"fonts/BebasNeue-Regular.woff2"}, Version: "2.000", SPDX: "OFL-1.1", URL: "https://github.com/dharmatype/Bebas-Neue", LicenseFile: "bebas-neue.txt"},
	{Name: "Inter", Files: []string{"fonts/InterVariable.woff2"}, Version: "4.1", SPDX: "OFL-1.1", URL: "https://github.com/rsms/inter", LicenseFile: "inter.txt"},
	{Name: "Geist Mono", Files: []string{"fonts/GeistMono-wght.woff2"}, Version: "1.9.0", SPDX: "OFL-1.1", URL: "https://github.com/vercel/geist-font", LicenseFile: "geist-mono.txt"},
	{Name: "Go (standard library and runtime)", Version: runtime.Version(), SPDX: "BSD-3-Clause", URL: "https://go.dev", LicenseFile: "go.txt"},
	{Name: "golang.org/x/crypto", Version: ModuleVersion("golang.org/x/crypto", "v0.40.0"), SPDX: "BSD-3-Clause", URL: "https://pkg.go.dev/golang.org/x/crypto", LicenseFile: "x-crypto.txt"},
	{Name: "github.com/godbus/dbus/v5", Version: ModuleVersion("github.com/godbus/dbus/v5", "v5.1.0"), SPDX: "BSD-2-Clause", URL: "https://github.com/godbus/dbus", LicenseFile: "godbus.txt"},
}

// LicenseText returns the full AGPL text of this program.
func LicenseText() string {
	b, _ := fsys.ReadFile("LICENSE")
	return string(b)
}

// noticesText returns the plain text THIRD-PARTY-NOTICES file.
func noticesText() string {
	b, _ := fsys.ReadFile("THIRD-PARTY-NOTICES")
	return string(b)
}

// ComponentLicense returns the license text of a component.
func ComponentLicense(c Component) (string, error) {
	b, err := fsys.ReadFile("licenses/" + c.LicenseFile)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ModuleVersion returns the version of a Go module from the build info.
// It returns fallback if the version is not known, for example in tests.
func ModuleVersion(path, fallback string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fallback
	}
	for _, d := range info.Deps {
		if d.Path == path && d.Version != "" {
			return d.Version
		}
	}
	return fallback
}

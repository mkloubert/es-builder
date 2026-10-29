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

package priv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// USBDevice is one USB stick partition that the helper can mount.
type USBDevice struct {
	Path, FSType, Label string
	Size                int64
}

var (
	usbPathRe  = regexp.MustCompile(`^/dev/[a-z0-9]+$`)
	usbFSTypes = map[string]bool{"vfat": true, "exfat": true, "ntfs": true}
	errNoMount = errors.New("usb-mount gave no mount point")
)

// USBList lists the USB stick partitions. Lines that do not match the
// format "<path>\t<fstype>\t<size>\t<label>" are ignored.
func (p Priv) USBList(ctx context.Context) ([]USBDevice, error) {
	var buf bytes.Buffer
	if err := p.run(ctx, &buf, "usb-list"); err != nil {
		return nil, err
	}
	out := []USBDevice{}
	for _, line := range strings.Split(buf.String(), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || !usbPathRe.MatchString(f[0]) || !usbFSTypes[f[1]] {
			continue
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || size < 0 {
			continue
		}
		label := f[3]
		if label == "-" {
			label = ""
		}
		out = append(out, USBDevice{Path: f[0], FSType: f[1], Label: label, Size: size})
	}
	return out, nil
}

// USBMount mounts the partition at path and returns the mount point.
func (p Priv) USBMount(ctx context.Context, path string) (string, error) {
	if !usbPathRe.MatchString(path) {
		return "", fmt.Errorf("invalid device %q", path)
	}
	var buf bytes.Buffer
	if err := p.run(ctx, &buf, "usb-mount", path); err != nil {
		return "", err
	}
	dir := strings.TrimSpace(buf.String())
	if dir == "" {
		return "", errNoMount
	}
	return dir, nil
}

// USBUmount unmounts the USB stick. It is fine when nothing is mounted.
func (p Priv) USBUmount(ctx context.Context) error {
	return p.run(ctx, nil, "usb-umount")
}

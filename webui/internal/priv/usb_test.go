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
	"context"
	"reflect"
	"testing"
)

func TestUSBListParsesLines(t *testing.T) {
	f := &fakeRunner{out: "/dev/sda1\tvfat\t16000000000\tSTICK\n" +
		"/dev/sdb1\texfat\t1024\t-\n" +
		"\n" +
		"broken line\n" +
		"/dev/sdc1\text4\t10\tX\n" +
		"/dev/sdd1\tntfs\tabc\tX\n" +
		"/dev/sde1\tntfs\t5\tMy Label\n"}
	got, err := (Priv{R: f, Helper: DefaultHelper}).USBList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []USBDevice{
		{Path: "/dev/sda1", FSType: "vfat", Size: 16000000000, Label: "STICK"},
		{Path: "/dev/sdb1", FSType: "exfat", Size: 1024, Label: ""},
		{Path: "/dev/sde1", FSType: "ntfs", Size: 5, Label: "My Label"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	h := DefaultHelper
	if !reflect.DeepEqual(f.calls, [][]string{{"sudo", "-n", h, "usb-list"}}) {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestUSBListEmpty(t *testing.T) {
	got, err := (Priv{R: &fakeRunner{}, Helper: DefaultHelper}).USBList(context.Background())
	if err != nil || got == nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestUSBMountAndUmountCommandLines(t *testing.T) {
	f := &fakeRunner{out: "/run/emulationstation-usb\n"}
	p := Priv{R: f, Helper: DefaultHelper}
	dir, err := p.USBMount(context.Background(), "/dev/sda1")
	if err != nil || dir != "/run/emulationstation-usb" {
		t.Fatal(dir, err)
	}
	if err := p.USBUmount(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := DefaultHelper
	want := [][]string{{"sudo", "-n", h, "usb-mount", "/dev/sda1"}, {"sudo", "-n", h, "usb-umount"}}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestUSBMountRejectsBadPath(t *testing.T) {
	f := &fakeRunner{out: "/run/emulationstation-usb\n"}
	p := Priv{R: f, Helper: DefaultHelper}
	for _, bad := range []string{"", "/dev/../etc", "sda1", "/dev/sda1 x", "/dev/SDA1"} {
		if _, err := p.USBMount(context.Background(), bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
	if len(f.calls) != 0 {
		t.Fatal("runner called")
	}
}

func TestUSBMountEmptyOutputFails(t *testing.T) {
	p := Priv{R: &fakeRunner{out: "\n"}, Helper: DefaultHelper}
	if _, err := p.USBMount(context.Background(), "/dev/sda1"); err == nil {
		t.Fatal("want error")
	}
}

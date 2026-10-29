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

package controllers

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCRC16Check(t *testing.T) {
	if got := CRC16([]byte("123456789")); got != 0xBB3D {
		t.Fatalf("got %#x", got)
	}
}

func TestGUIDXbox360NoCRC(t *testing.T) {
	got := SDLGUID(3, 0x045e, 0x028e, 0x0114, "Microsoft X-Box 360 pad", false)
	if got != "030000005e0400008e02000014010000" {
		t.Fatal(got)
	}
}

func TestGUIDWithCRCSetsBytes2and3(t *testing.T) {
	name := "Microsoft X-Box 360 pad"
	no := SDLGUID(3, 0x045e, 0x028e, 0x0114, name, false)
	with := SDLGUID(3, 0x045e, 0x028e, 0x0114, name, true)
	crc := CRC16([]byte(name))
	want := no[:4] + string("0123456789abcdef"[crc>>4&0xf]) + string("0123456789abcdef"[crc&0xf]) +
		string("0123456789abcdef"[crc>>12]) + string("0123456789abcdef"[crc>>8&0xf]) + no[8:]
	if with != want {
		t.Fatalf("got %s want %s", with, want)
	}
	if with[:4] != no[:4] || with[8:] != no[8:] {
		t.Fatal("other bytes differ")
	}
}

func TestEnumerateFakeSys(t *testing.T) {
	devs, err := Enumerate("testdata/sys")
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("got %d devices", len(devs))
	}
	if devs[0].Name != "Microsoft X-Box 360 pad" || devs[0].Bus != "usb" || devs[0].GUID != "030000005e0400008e02000014010000" {
		t.Fatalf("%+v", devs[0])
	}
	if devs[1].Bus != "bluetooth" || devs[1].Vendor != 0x2dc8 || devs[1].GUIDCRC == devs[1].GUID {
		t.Fatalf("%+v", devs[1])
	}
}

func TestMatchFallbacks(t *testing.T) {
	c, err := ReadInputCfg("testdata/es_input.cfg")
	if err != nil {
		t.Fatal(err)
	}
	d := Device{Name: "Other", GUID: "030000005E0400008E02000014010000", GUIDCRC: "03001234" + "5e0400008e02000014010000"}
	if _, ok := c.Match(d); !ok {
		t.Fatal("no match via GUID")
	}
	d = Device{Name: "Microsoft X-Box 360 pad", GUID: "x", GUIDCRC: "y"}
	if _, ok := c.Match(d); !ok {
		t.Fatal("no match via name")
	}
	if _, ok := c.Match(Device{Name: "Keyboard", GUID: "-1", GUIDCRC: "-1"}); ok {
		t.Fatal("keyboard must not match")
	}
	devs := []Device{d, {Name: "n", GUID: "g", GUIDCRC: "c"}}
	MarkConfigured(devs, c)
	if !devs[0].Configured || devs[1].Configured {
		t.Fatalf("%+v", devs)
	}
}

func TestRemoveAndUpsertRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "es_input.cfg")
	orig, _ := os.ReadFile("testdata/es_input.cfg")
	os.WriteFile(p, orig, 0o644)
	c, err := ReadInputCfg(p)
	if err != nil {
		t.Fatal(err)
	}
	d := Device{Name: "Microsoft X-Box 360 pad", GUID: "030000005e0400008e02000014010000"}
	if !c.Remove(d) || c.Remove(d) {
		t.Fatal("remove semantics")
	}
	node := []byte(`<inputConfig type="joystick" deviceName="Pad" deviceGUID="aa"><input name="a" type="button" id="1" value="1" /></inputConfig>`)
	if err := c.Upsert(node); err != nil {
		t.Fatal(err)
	}
	if err := c.Upsert(node); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(p); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(p + ".bak")
	if err != nil || string(bak) != string(orig) {
		t.Fatalf("bak: %v", err)
	}
	out, _ := os.ReadFile(p)
	s := string(out)
	for _, want := range []string{`type="keyboard"`, `<inputAction type="onfinish">`, `deviceGUID="aa"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in\n%s", want, s)
		}
	}
	if strings.Count(s, `deviceGUID="aa"`) != 1 || strings.Contains(s, "030000005e") {
		t.Fatalf("bad content:\n%s", s)
	}
	if !strings.HasPrefix(s, "<?xml version=\"1.0\"?>\n<inputList>\n") || !strings.HasSuffix(s, "</inputList>\n") {
		t.Fatal(s)
	}
	if strings.Index(s, "keyboard") > strings.Index(s, "onfinish") || strings.Index(s, "onfinish") > strings.Index(s, `"aa"`) {
		t.Fatalf("order not preserved:\n%s", s)
	}
	if _, err := ReadInputCfg(p); err != nil {
		t.Fatal(err)
	}
}

func TestReadMissingIsEmpty(t *testing.T) {
	c, err := ReadInputCfg(filepath.Join(t.TempDir(), "none.cfg"))
	if err != nil || len(c.nodes) != 0 {
		t.Fatal(c, err)
	}
}

func TestUpsertRejectsInvalidXML(t *testing.T) {
	c := &InputCfg{}
	for _, bad := range []string{
		`<inputConfig type="joystick" deviceName="a" deviceGUID="b">`,
		`<inputConfig type="keyboard" deviceName="a" deviceGUID="b"/>`,
		`<inputConfig type="joystick" deviceName="a"/>`,
		`<foo type="joystick" deviceName="a" deviceGUID="b"/>`,
		`<inputConfig type="joystick" deviceName="a" deviceGUID="b"/><x/>`,
		``,
	} {
		if err := c.Upsert([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if len(c.nodes) != 0 {
		t.Fatal("nodes added")
	}
}

type fakeRunner struct {
	name string
	args []string
	file string
	dir  string
}

func (f *fakeRunner) Run(_ context.Context, _ io.Writer, name string, args ...string) error {
	f.name, f.args = name, args
	b, _ := os.ReadFile(filepath.Join(f.dir, "es_temporaryinput.cfg"))
	f.file = string(b)
	return nil
}

func TestApplyHookWritesTempFile(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{dir: dir}
	node := []byte(`<inputConfig type="joystick" deviceName="a" deviceGUID="b"/>`)
	if err := ApplyHook(context.Background(), r, dir, "/hook.sh", node); err != nil {
		t.Fatal(err)
	}
	want := "<?xml version=\"1.0\"?>\n<inputList>\n" + string(node) + "\n</inputList>\n"
	if r.file != want {
		t.Fatalf("got %q", r.file)
	}
	if r.name != "bash" || len(r.args) != 1 || r.args[0] != "/hook.sh" {
		t.Fatalf("%s %v", r.name, r.args)
	}
}

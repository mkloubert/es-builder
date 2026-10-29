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
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	out   string
}

func (f *fakeRunner) Run(ctx context.Context, stdout io.Writer, name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	if stdout != nil {
		io.WriteString(stdout, f.out)
	}
	return nil
}

func TestPrivCommandLines(t *testing.T) {
	f := &fakeRunner{}
	p := Priv{R: f, Helper: DefaultHelper}
	ctx := context.Background()
	p.Unban(ctx, "1.2.3.4")
	p.Reboot(ctx)
	p.Poweroff(ctx)
	p.AptUpdate(ctx, io.Discard)
	p.AptUpgrade(ctx, io.Discard)
	p.Journal(ctx, "ssh")
	h := "/usr/local/lib/emulationstation/priv"
	want := [][]string{
		{"sudo", "-n", h, "f2b-unban", "1.2.3.4"},
		{"sudo", "-n", h, "reboot"},
		{"sudo", "-n", h, "poweroff"},
		{"sudo", "-n", h, "apt-update"},
		{"sudo", "-n", h, "apt-upgrade"},
		{"sudo", "-n", h, "journal", "ssh"},
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("got %v", f.calls)
	}
}

func TestUnbanRejectsGarbage(t *testing.T) {
	f := &fakeRunner{}
	if err := (Priv{R: f, Helper: DefaultHelper}).Unban(context.Background(), "1.2.3.4; rm -rf /"); err == nil {
		t.Fatal("want error")
	}
	if len(f.calls) != 0 {
		t.Fatal("runner called")
	}
}

func TestJournalRejectsUnknownUnit(t *testing.T) {
	f := &fakeRunner{}
	if _, err := (Priv{R: f, Helper: DefaultHelper}).Journal(context.Background(), "cron"); err == nil {
		t.Fatal("want error")
	}
	if len(f.calls) != 0 {
		t.Fatal("runner called")
	}
}

func TestJournalReturnsOutput(t *testing.T) {
	f := &fakeRunner{out: "line\n"}
	s, err := (Priv{R: f, Helper: DefaultHelper}).Journal(context.Background(), "fail2ban")
	if err != nil || s != "line\n" {
		t.Fatal(s, err)
	}
}

func TestBansParsing(t *testing.T) {
	f := &fakeRunner{out: "sshd 1.2.3.4\n\nbroken\nx notanip\nemulationstation-web 5.6.7.8\n"}
	got, err := (Priv{R: f, Helper: DefaultHelper}).Bans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Ban{{"sshd", "1.2.3.4"}, {"emulationstation-web", "5.6.7.8"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	f.out = ""
	got, err = (Priv{R: f, Helper: DefaultHelper}).Bans(context.Background())
	if err != nil || got == nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestExecRunnerStderrInError(t *testing.T) {
	var sb strings.Builder
	err := ExecRunner{}.Run(context.Background(), &sb, "sh", "-c", "echo out; echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") || sb.String() != "out\n" {
		t.Fatal(err, sb.String())
	}
}

func TestReadESLog(t *testing.T) {
	home := t.TempDir()
	if s, err := ReadESLog(home); s != "" || err != nil {
		t.Fatal(s, err)
	}
	d := filepath.Join(home, ".emulationstation")
	os.MkdirAll(d, 0o755)
	var sb strings.Builder
	for i := 1; i <= 600; i++ {
		sb.WriteString("l" + strings.Repeat("x", 1) + "\n")
	}
	os.WriteFile(filepath.Join(d, "es_log.txt"), []byte(sb.String()), 0o644)
	s, err := ReadESLog(home)
	if err != nil || strings.Count(s, "\n") != 500 {
		t.Fatal(strings.Count(s, "\n"), err)
	}
}

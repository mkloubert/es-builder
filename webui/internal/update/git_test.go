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
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func has(list []string, want ...string) bool {
	return strings.Contains("\x00"+strings.Join(list, "\x00")+"\x00", "\x00"+strings.Join(want, "\x00")+"\x00")
}

func TestGitArgsHTTPS(t *testing.T) {
	a := gitArgs(Settings{URL: "https://example.com/x"}, "ls-remote", "--", "https://example.com/x")
	for _, w := range []string{"protocol.allow=never", "protocol.https.allow=always", "protocol.ssh.allow=always", "core.hooksPath=/dev/null"} {
		if !has(a, "-c", w) {
			t.Errorf("missing %s in %v", w, a)
		}
	}
	for _, x := range a {
		if strings.HasPrefix(x, "safe.directory") || x == "protocol.file.allow=always" {
			t.Errorf("unexpected %s", x)
		}
	}
	if !has(a, "--", "https://example.com/x") {
		t.Errorf("no -- before url: %v", a)
	}
}

func TestGitArgsLocal(t *testing.T) {
	for _, u := range []string{"/srv/repo", "file:///srv/repo"} {
		a := gitArgs(Settings{URL: u}, "ls-remote")
		if !has(a, "-c", "protocol.file.allow=always") || !has(a, "-c", "safe.directory=/srv/repo") {
			t.Errorf("%s: %v", u, a)
		}
	}
}

func TestGitEnv(t *testing.T) {
	e := gitEnv(Settings{})
	for _, w := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_ASKPASS=/bin/false"} {
		if !slices.Contains(e, w) {
			t.Errorf("missing %s", w)
		}
	}
	if !slices.Contains(e, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=yes") {
		t.Errorf("ssh command: %v", e)
	}
	e = gitEnv(Settings{SSHKeyFile: "/k", KnownHostsFile: "/h"})
	want := "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=yes -i /k -o IdentitiesOnly=yes -o UserKnownHostsFile=/h"
	if !slices.Contains(e, want) {
		t.Errorf("got %v", e)
	}
	e = gitEnv(Settings{KnownHostsFile: "/h"})
	if !slices.Contains(e, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/h") {
		t.Errorf("got %v", e)
	}
}

func TestLsRemoteCall(t *testing.T) {
	f := &fakeRunner{fn: func(c call, w io.Writer) error {
		io.WriteString(w, "abc\trefs/tags/webui-v1.0.0\n")
		return nil
	}}
	g := Git{R: f, S: Settings{URL: "https://example.com/x"}}
	out, err := g.LsRemote(context.Background())
	if err != nil || !strings.Contains(out, "webui-v1.0.0") {
		t.Fatalf("%q %v", out, err)
	}
	c := f.calls[0]
	if c.Name != "git" || !has(c.Args, "ls-remote", "--tags", "--", "https://example.com/x") {
		t.Errorf("%v", c)
	}
	if !slices.Contains(c.Env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("env %v", c.Env)
	}
}

func TestLsRemoteBadURL(t *testing.T) {
	f := &fakeRunner{}
	g := Git{R: f, S: Settings{URL: "ext::sh -c x"}}
	if _, err := g.LsRemote(context.Background()); err == nil || len(f.calls) != 0 {
		t.Fatalf("err %v calls %d", err, len(f.calls))
	}
}

func TestCloneRefusesBadInput(t *testing.T) {
	f := &fakeRunner{}
	g := Git{R: f, S: Settings{URL: "https://example.com/x"}}
	dir := filepath.Join(t.TempDir(), "c")
	for _, tag := range []string{"", "v1.0.0", "webui-v1.0", "webui-v01.0.0", "--upload-pack=x", "webui-v1.0.0 "} {
		if err := g.Clone(context.Background(), tag, dir); err == nil {
			t.Errorf("tag %q accepted", tag)
		}
	}
	existing := t.TempDir()
	if err := g.Clone(context.Background(), "webui-v1.0.0", existing); err == nil {
		t.Error("existing dir accepted")
	}
	if len(f.calls) != 0 {
		t.Errorf("git was called: %v", f.calls)
	}
}

func TestCloneSteps(t *testing.T) {
	f := &fakeRunner{fn: func(c call, w io.Writer) error {
		if has(c.Args, "rev-parse") {
			io.WriteString(w, "deadbeef\n")
		}
		return nil
	}}
	src := t.TempDir()
	g := Git{R: f, S: Settings{URL: src, SignersFile: "/etc/signers"}}
	dir := filepath.Join(t.TempDir(), "c")
	if err := g.Clone(context.Background(), "webui-v1.2.3", dir); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 5 {
		t.Fatalf("calls: %d", len(f.calls))
	}
	cl := f.calls[0].Args
	if !has(cl, "clone", "--depth", "1", "--no-recurse-submodules", "--branch", "webui-v1.2.3", "--no-local", "--", src, dir) {
		t.Errorf("clone args %v", cl)
	}
	if !has(f.calls[1].Args, "rev-parse", "refs/tags/webui-v1.2.3^{commit}") || f.calls[1].Dir != dir {
		t.Errorf("%v", f.calls[1])
	}
	if !has(f.calls[2].Args, "rev-parse", "HEAD") {
		t.Errorf("%v", f.calls[2])
	}
	if !has(f.calls[3].Args, "cat-file", "-t", "refs/tags/webui-v1.2.3") {
		t.Errorf("%v", f.calls[3])
	}
	v := f.calls[4].Args
	if !has(v, "-c", "gpg.format=ssh") || !has(v, "-c", "gpg.ssh.allowedSignersFile=/etc/signers") || !has(v, "verify-tag", "refs/tags/webui-v1.2.3") {
		t.Errorf("verify args %v", v)
	}
}

func TestCloneHTTPSNoLocalFlagNoVerify(t *testing.T) {
	f := &fakeRunner{fn: func(c call, w io.Writer) error {
		io.WriteString(w, "deadbeef\n")
		return nil
	}}
	g := Git{R: f, S: Settings{URL: "https://example.com/x"}}
	if err := g.Clone(context.Background(), "webui-v1.2.3", filepath.Join(t.TempDir(), "c")); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.calls[0].Args, "--no-local") {
		t.Error("--no-local for https")
	}
	for _, c := range f.calls {
		if slices.Contains(c.Args, "verify-tag") {
			t.Error("verify without signers file")
		}
	}
}

func TestCloneTagNotAtHead(t *testing.T) {
	n := 0
	f := &fakeRunner{fn: func(c call, w io.Writer) error {
		if has(c.Args, "rev-parse") {
			n++
			io.WriteString(w, strings.Repeat("a", n)+"\n")
		}
		return nil
	}}
	g := Git{R: f, S: Settings{URL: "https://example.com/x"}}
	if err := g.Clone(context.Background(), "webui-v1.2.3", filepath.Join(t.TempDir(), "c")); err == nil {
		t.Error("mismatch accepted")
	}
}

func TestCloneVerifyFailIsUnsigned(t *testing.T) {
	f := &fakeRunner{fn: func(c call, w io.Writer) error {
		if slices.Contains(c.Args, "verify-tag") {
			return errors.New("bad signature")
		}
		io.WriteString(w, "x\n")
		return nil
	}}
	g := Git{R: f, S: Settings{URL: "https://example.com/x", SignersFile: "/s"}}
	err := g.Clone(context.Background(), "webui-v1.2.3", filepath.Join(t.TempDir(), "c"))
	if !errors.Is(err, ErrUnsigned) {
		t.Errorf("got %v", err)
	}
}

func TestExecRunner(t *testing.T) {
	var out strings.Builder
	err := ExecRunner{}.Run(context.Background(), t.TempDir(), []string{"FOO=bar"}, &out, "sh", "-c", "echo $FOO; pwd >/dev/null")
	if err != nil || strings.TrimSpace(out.String()) != "bar" {
		t.Fatalf("%q %v", out.String(), err)
	}
	err = ExecRunner{}.Run(context.Background(), "", nil, nil, "sh", "-c", "echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("stderr not in error: %v", err)
	}
}

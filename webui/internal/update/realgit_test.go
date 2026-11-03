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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs a real git command in dir for test setup.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=t@example.com", "-c", "protocol.file.allow=always"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// makeKey creates an ed25519 key pair and returns the private key path and the public line.
func makeKey(t *testing.T, dir, name string) (string, string) {
	t.Helper()
	key := filepath.Join(dir, name)
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return key, strings.TrimSpace(string(pub))
}

func TestRealGit(t *testing.T) {
	for _, tool := range []string{"git", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is missing", tool)
		}
	}
	// Keep git away from the real HOME and config.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")

	keys := t.TempDir()
	keyA, pubA := makeKey(t, keys, "a")
	keyB, _ := makeKey(t, keys, "b")
	signers := filepath.Join(keys, "allowed_signers")
	if err := os.WriteFile(signers, []byte("* "+pubA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	gitIn(t, work, "init", "-q", "-b", "main")
	commit := func(name string) {
		if err := os.WriteFile(filepath.Join(work, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		gitIn(t, work, "add", name)
		gitIn(t, work, "commit", "-q", "-m", name)
	}
	sign := func(key string) []string {
		return []string{"-c", "gpg.format=ssh", "-c", "user.signingkey=" + key}
	}
	commit("one")
	gitIn(t, work, "tag", "webui-v0.1.0")
	commit("two")
	gitIn(t, work, append(sign(keyA), "tag", "-s", "-m", "v2", "webui-v0.2.0")...)
	commit("three")
	gitIn(t, work, append(sign(keyB), "tag", "-s", "-m", "v3", "webui-v0.3.0")...)
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitIn(t, work, "clone", "-q", "--bare", work, bare)

	ctx := context.Background()
	url := "file://" + bare

	g := Git{R: ExecRunner{}, S: Settings{URL: url, SignersFile: signers}}
	out, err := g.LsRemote(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := LatestTag(out); !ok || v.String() != "0.3.0" {
		t.Fatalf("latest %v %v", v, ok)
	}

	// The repo is owned by this user, but safe.directory must still be in the args.
	if !has(gitArgs(g.S, "x"), "-c", "safe.directory="+bare) {
		t.Error("no safe.directory")
	}

	// Replay: the signed object of v0.2.0 under the name v0.9.0.
	// Branch shadow: a branch named like a tag that has no tag ref.
	tagObj := strings.TrimSpace(gitIn(t, bare, "rev-parse", "refs/tags/webui-v0.2.0"))
	gitIn(t, bare, "update-ref", "refs/tags/webui-v0.9.0", tagObj)
	gitIn(t, bare, "update-ref", "refs/heads/webui-v0.8.0", "refs/heads/main")

	dir := filepath.Join(t.TempDir(), "ok")
	if err := g.Clone(ctx, "webui-v0.2.0", dir); err != nil {
		t.Fatalf("signed clone: %v", err)
	}
	if got := strings.TrimSpace(gitIn(t, dir, "cat-file", "-t", "refs/tags/webui-v0.2.0")); got != "tag" {
		t.Errorf("tag object type %q", got)
	}

	for _, tag := range []string{"webui-v0.3.0", "webui-v0.1.0"} {
		err := g.Clone(ctx, tag, filepath.Join(t.TempDir(), "x"))
		if !errors.Is(err, ErrUnsigned) {
			t.Errorf("%s: got %v want ErrUnsigned", tag, err)
		}
	}

	open := Git{R: ExecRunner{}, S: Settings{URL: url}}
	for _, tag := range []string{"webui-v0.1.0", "webui-v0.2.0", "webui-v0.3.0"} {
		if err := open.Clone(ctx, tag, filepath.Join(t.TempDir(), "y")); err != nil {
			t.Errorf("%s without signers: %v", tag, err)
		}
	}

	for _, withSigners := range []bool{true, false} {
		s := Settings{URL: url}
		if withSigners {
			s.SignersFile = signers
		}
		rg := Git{R: ExecRunner{}, S: s}
		for _, tag := range []string{"webui-v0.9.0", "webui-v0.8.0"} {
			if err := rg.Clone(ctx, tag, filepath.Join(t.TempDir(), "r")); err == nil {
				t.Errorf("%s (signers=%v) was accepted", tag, withSigners)
			}
		}
	}

	// A plain path works too.
	plain := Git{R: ExecRunner{}, S: Settings{URL: bare, SignersFile: signers}}
	if err := plain.Clone(ctx, "webui-v0.2.0", filepath.Join(t.TempDir(), "z")); err != nil {
		t.Errorf("plain path: %v", err)
	}

	c := &Checker{Git: g, File: filepath.Join(t.TempDir(), "check.json")}
	if s := c.Check(ctx); s.Err != "" || s.Latest != "0.9.0" {
		t.Errorf("check %+v", s)
	}
}

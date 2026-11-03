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
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Settings tells git where the updates are and how to reach them.
type Settings struct {
	URL            string
	SignersFile    string
	SSHKeyFile     string
	KnownHostsFile string
}

// Git runs git through a Runner. A nil R means ExecRunner.
type Git struct {
	R Runner
	S Settings
}

// ErrUnsigned means the tag has no valid signature from a trusted key.
var ErrUnsigned = errors.New("tag not signed by a trusted key")

const (
	lsRemoteTimeout = 60 * time.Second
	cloneTimeout    = 10 * time.Minute
	verifyTimeout   = 60 * time.Second
	quickTimeout    = 60 * time.Second
)

var pathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// checkPaths checks the file paths that go into the git command line or the
// ssh command. Empty paths are fine.
func checkPaths(s Settings) error {
	for _, p := range []string{s.SignersFile, s.SSHKeyFile, s.KnownHostsFile} {
		if p == "" {
			continue
		}
		if !pathRe.MatchString(p) {
			return errors.New("file path not allowed")
		}
		for _, part := range strings.Split(p, "/") {
			if part == ".." {
				return errors.New("file path not allowed")
			}
		}
	}
	return nil
}

// gitEnv returns the environment for every git call.
func gitEnv(s Settings) []string {
	ssh := "ssh -o BatchMode=yes -o StrictHostKeyChecking=yes"
	if s.SSHKeyFile != "" {
		ssh += " -i " + s.SSHKeyFile + " -o IdentitiesOnly=yes"
	}
	if s.KnownHostsFile != "" {
		ssh += " -o UserKnownHostsFile=" + s.KnownHostsFile
	}
	return []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_ASKPASS=/bin/false",
		"GIT_SSH_COMMAND=" + ssh,
	}
}

// gitArgs puts the fixed safe options before the git sub command and its args.
// The caller puts "--" before the URL.
func gitArgs(s Settings, sub ...string) []string {
	a := []string{
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "protocol.ssh.allow=always",
	}
	if k, err := CheckURL(s.URL); err == nil && k == KindLocal {
		a = append(a, "-c", "protocol.file.allow=always")
	}
	a = append(a, "-c", "core.hooksPath=/dev/null")
	if p := LocalPath(s.URL); p != "" {
		a = append(a, "-c", "safe.directory="+p)
	}
	return append(a, sub...)
}

func (g Git) runner() Runner {
	if g.R == nil {
		return ExecRunner{}
	}
	return g.R
}

// run runs git with a timeout and returns its stdout.
func (g Git) run(ctx context.Context, timeout time.Duration, dir string, sub ...string) (string, error) {
	if err := checkPaths(g.S); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out bytes.Buffer
	err := g.runner().Run(ctx, dir, gitEnv(g.S), &out, "git", gitArgs(g.S, sub...)...)
	return out.String(), err
}

// LsRemote returns the output of "git ls-remote --tags" for the update URL.
func (g Git) LsRemote(ctx context.Context) (string, error) {
	if _, err := CheckURL(g.S.URL); err != nil {
		return "", err
	}
	if err := checkPaths(g.S); err != nil {
		return "", err
	}
	return g.run(ctx, lsRemoteTimeout, "", "ls-remote", "--tags", "--", g.S.URL)
}

// Clone fetches the tag (like "webui-v1.2.3") into dir. dir must not exist.
// It checks that the tag points to HEAD. With a signers file, it also checks
// the signature and returns ErrUnsigned if that fails.
func (g Git) Clone(ctx context.Context, tag, dir string) error {
	if _, ok := ParseTag(tag); !ok {
		return errors.New("bad tag name")
	}
	k, err := CheckURL(g.S.URL)
	if err != nil {
		return err
	}
	if err := checkPaths(g.S); err != nil {
		return err
	}
	if _, err := os.Lstat(dir); err == nil {
		return errors.New("clone folder exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	sub := []string{"clone", "--depth", "1", "--no-recurse-submodules", "--branch", tag}
	if k == KindLocal {
		sub = append(sub, "--no-local")
	}
	sub = append(sub, "--", g.S.URL, dir)
	if _, err := g.run(ctx, cloneTimeout, "", sub...); err != nil {
		return err
	}
	ref := "refs/tags/" + tag
	tagCommit, err := g.run(ctx, quickTimeout, dir, "rev-parse", ref+"^{commit}")
	if err != nil {
		return err
	}
	head, err := g.run(ctx, quickTimeout, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if t := strings.TrimSpace(tagCommit); t == "" || t != strings.TrimSpace(head) {
		return fmt.Errorf("tag %s does not point to HEAD", tag)
	}
	if err := g.checkTagObject(ctx, tag, ref, dir); err != nil {
		return err
	}
	if g.S.SignersFile == "" {
		return nil
	}
	if _, err := g.run(ctx, verifyTimeout, dir,
		"-c", "gpg.format=ssh", "-c", "gpg.ssh.allowedSignersFile="+g.S.SignersFile,
		"verify-tag", ref); err != nil {
		return ErrUnsigned
	}
	return nil
}

// checkTagObject makes sure a tag object really belongs to its name. A signed
// tag object copied to another ref name must not pass. A light tag has no
// object and no header, so it is left to the signature check.
func (g Git) checkTagObject(ctx context.Context, tag, ref, dir string) error {
	typ, err := g.run(ctx, quickTimeout, dir, "cat-file", "-t", ref)
	if err != nil {
		return err
	}
	if strings.TrimSpace(typ) != "tag" {
		return nil
	}
	body, err := g.run(ctx, quickTimeout, dir, "cat-file", "tag", ref)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			break
		}
		if name, ok := strings.CutPrefix(line, "tag "); ok {
			if name == tag {
				return nil
			}
			break
		}
	}
	return ErrUnsigned
}

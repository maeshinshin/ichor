/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package prmode

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// gitAuthUser is the HTTP Basic auth username sent alongside token: the
// convention GitHub documents for a token-as-password login (a token is
// the actual credential; the username is not otherwise checked), used
// here for all three hosts since HTTP Basic auth with a token as the
// password is the one git-smart-HTTP auth mechanism all three document,
// unlike any single header scheme.
const gitAuthUser = "x-access-token"

// gitAuthEnv returns the environment variables that make every `git`
// subprocess below send token as an HTTP Basic credential, without ever
// putting it in argv (a process's command line is visible to anyone on
// the box via `ps`; its environment is not, short of root or the same
// uid) or in a file on disk.
//
// GIT_CONFIG_COUNT/_KEY_n/_VALUE_n is git's own documented mechanism
// (git-config(1), "ENVIRONMENT") for injecting config -- here,
// http.extraHeader, git's own mechanism for an extra HTTP request header
// -- without a config file or a `-c` command-line argument (which would
// put token in argv exactly as above). GIT_TERMINAL_PROMPT=0 makes a
// credential git cannot authenticate with fail outright instead of
// hanging on an interactive prompt; GIT_TRACE*/GIT_CURL_VERBOSE are
// explicitly cleared (not just left unset) because any of them would
// print header values -- including this one -- to the command's own
// output, which callers do log.
func gitAuthEnv(token string) []string {
	header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(gitAuthUser+":"+token))

	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=" + header,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_TRACE=",
		"GIT_TRACE_PACKET=",
		"GIT_TRACE_CURL=",
		"GIT_CURL_VERBOSE=",
	}
}

// runGit runs `git args...` in cwd with token wired in via gitAuthEnv.
// args never contains token -- every call site below passes it only
// through env -- so the combined output embedded in a non-nil return is
// always safe to include in a returned error or to log.
func runGit(ctx context.Context, cwd, token string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), gitAuthEnv(token)...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("prmode: git %v: %w: %s", args, err, out)
	}

	return nil
}

// Clone shallow-clones cloneURL's ref branch into dir (which must not
// already exist), authenticating with token. A shallow, single-branch
// clone is enough: Locate/Patch only ever need that branch's current
// tip, and the new branch CommitAndPush pushes from it still gets a full
// diff against base on the host's own side, which has the complete
// history already.
func Clone(ctx context.Context, cloneURL, ref, token, dir string) error {
	return runGit(ctx, "", token,
		"clone", "--branch", ref, "--depth", "1", "--single-branch", cloneURL, dir)
}

// CommitAndPush checks out a new branch in dir (an existing checkout,
// e.g. from Clone), writes files (path relative to dir -> new content),
// commits them with message, and pushes the branch to the clone's
// origin, authenticating with token.
func CommitAndPush(ctx context.Context, dir, branch, token, message string, files map[string][]byte) error {
	if err := runGit(ctx, dir, token, "checkout", "-b", branch); err != nil {
		return err
	}

	for relPath, content := range files {
		path := filepath.Join(dir, relPath)

		if err := os.WriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("prmode: writing %s: %w", path, err)
		}

		if err := runGit(ctx, dir, token, "add", "--", relPath); err != nil {
			return err
		}
	}

	if err := runGit(ctx, dir, token, "commit", "-m", message); err != nil {
		return err
	}

	return runGit(ctx, dir, token, "push", "origin", branch)
}

package dockerapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Checkout clones a repository into dir at a commit, branch or tag, and
// returns the full commit. A token, when given, is sent as HTTP basic auth
// for https repositories and never written to disk or the command line.
func Checkout(ctx context.Context, repo, commit, dir, token string) (string, error) {
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir + ".home", "GIT_TERMINAL_PROMPT=0"}
		if token != "" {
			// Passed through the environment, so it isn't in the process list.
			cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader",
				"GIT_CONFIG_VALUE_0=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token)))
		}
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(errb.String())
			if token != "" {
				msg = strings.ReplaceAll(msg, token, "***")
			}
			return "", fmt.Errorf("git %s: %v: %s", args[0], err, msg)
		}
		return strings.TrimSpace(out.String()), nil
	}
	if _, err := git("clone", "--quiet", "--", repo, dir); err != nil {
		return "", err
	}
	if _, err := git("-C", dir, "checkout", "--quiet", "--detach", commit); err != nil {
		return "", err
	}
	return git("-C", dir, "rev-parse", "HEAD")
}

// ResolveRemote resolves a branch or tag to its commit without cloning; a
// full commit is returned as it is.
func ResolveRemote(ctx context.Context, repo, ref, token string) (string, error) {
	if len(ref) == 40 && strings.Trim(ref, "0123456789abcdef") == "" {
		return ref, nil
	}
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--", repo, ref)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_TERMINAL_PROMPT=0"}
	if token != "" {
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token)))
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s: %v", ref, err)
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return "", fmt.Errorf("%s has no %q", repo, ref)
	}
	return f[0], nil
}

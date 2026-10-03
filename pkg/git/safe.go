package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Sandboxed agents write into git directories — a commit lands in the clone's
// .git — that unsandboxed processes (sidebars, the watcher, the CLI) then run
// git in. Repo-local config and hooks would let an agent turn that into code
// running outside its sandbox. Two defences, both here:
//
//   - HardenGit pins, for every git this process starts, the settings that
//     run a command and can be pinned without breaking anything: hooks,
//     fsmonitor and the ext transport off, and the ssh command, askpass and
//     credential helpers taken from the user's own (global/system) config
//     rather than whatever a repository says.
//   - RepoConfigRisk reports the repo-local settings that run a command and
//     cannot be pinned — filter drivers above all, whose names are the
//     repository's to choose — so callers refuse to touch such a repository.

// HardenGit sets GIT_CONFIG_* in this process's environment so every git it
// runs ignores repository-chosen commands. Call it once at startup of any
// unsandboxed command; it is idempotent.
func HardenGit() {
	if os.Getenv("SUPATREE_GIT_HARDENED") == "1" {
		return
	}
	pairs := [][2]string{
		{"core.hooksPath", "/dev/null"},
		{"core.fsmonitor", "false"},
		{"protocol.ext.allow", "never"},
		{"core.sshCommand", userConfig("core.sshCommand", "ssh")},
		{"core.askPass", userConfig("core.askPass", "")},
		// An empty value resets the helper list; the user's own helpers are
		// then added back, so https auth keeps working.
		{"credential.helper", ""},
	}
	for _, h := range userConfigAll("credential.helper") {
		pairs = append(pairs, [2]string{"credential.helper", h})
	}
	n := 0
	if v, err := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT")); err == nil {
		n = v
	}
	for _, p := range pairs {
		_ = os.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", n), p[0])
		_ = os.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", n), p[1])
		n++
	}
	_ = os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n))
	_ = os.Setenv("SUPATREE_GIT_HARDENED", "1")
}

// userConfig reads key from the user's own config — global and system, never
// a repository's — by asking git from outside any repository.
func userConfig(key, fallback string) string {
	if vals := userConfigAll(key); len(vals) > 0 {
		return vals[len(vals)-1]
	}
	return fallback
}

func userConfigAll(key string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "config", "--get-all", key)
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "GIT_DIR=/nonexistent", "GIT_CEILING_DIRECTORIES=/")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var vals []string
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if l != "" {
			vals = append(vals, l)
		}
	}
	return vals
}

// riskyPrefixes are repo-config keys that make git run a command and that
// HardenGit cannot pin, because their names are the repository's to choose.
var riskyPrefixes = []string{"filter.", "include.", "includeif.", "url.", "gpg.", "uploadpack.", "diff.", "merge."}

// riskyKeys are single keys of the same kind.
var riskyKeys = map[string]bool{"core.gitproxy": true, "ssh.variant": true}

// RepoConfigRisk lists the repository-local (and worktree-local) settings in
// dir's repository that would make an unsandboxed git run a command. Empty
// means safe to touch. An error reading the config is reported as a risk: the
// caller cannot vouch for a repository it cannot read.
func RepoConfigRisk(dir string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "config", "--list", "--show-scope").Output()
	if err != nil {
		return []string{fmt.Sprintf("cannot read the repository's config: %v", err)}
	}
	return riskyConfig(string(out))
}

func riskyConfig(list string) []string {
	var risky []string
	for _, line := range strings.Split(list, "\n") {
		scope, kv, ok := strings.Cut(line, "\t")
		if !ok || (scope != "local" && scope != "worktree") {
			continue
		}
		key, _, _ := strings.Cut(kv, "=")
		key = strings.ToLower(key)
		if riskyKeys[key] || isRiskyRemoteKey(key) {
			risky = append(risky, key)
			continue
		}
		for _, p := range riskyPrefixes {
			if strings.HasPrefix(key, p) {
				risky = append(risky, key)
				break
			}
		}
	}
	return risky
}

func isRiskyRemoteKey(key string) bool {
	return strings.HasPrefix(key, "remote.") &&
		(strings.HasSuffix(key, ".uploadpack") || strings.HasSuffix(key, ".receivepack"))
}

// RequireSafeRepo returns an error naming the settings RepoConfigRisk found.
func RequireSafeRepo(dir string) error {
	risky := RepoConfigRisk(dir)
	if len(risky) == 0 {
		return nil
	}
	return fmt.Errorf("refusing to run git in %s: its repository config sets %s, which would run a command outside "+
		"any sandbox — a sandboxed agent can write that file. Check it (git -C %s config --list --show-scope), "+
		"and if the setting is yours, move it to your global config",
		dir, strings.Join(risky, ", "), dir)
}

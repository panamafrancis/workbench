package supatree

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/testutil"
)

// oldLayout lays down what an older supatree left behind: ~/.supatree with a
// registry, a stack naming members by workbench alias, and workbench's config
// resolving those aliases to clones — one whose origin goes through a personal
// ssh alias.
func oldLayout(t *testing.T) (home, apiClone string) {
	t.Helper()
	home = testutil.IsolateHome(t)
	gitGlobal(t, "user.email", "t@example.com")
	gitGlobal(t, "user.name", "t")

	apiClone = originRepo(t)
	run(t, apiClone, "remote", "add", "origin", "git@github-work:fraud-zero/keystone-api.git")
	if err := os.WriteFile(filepath.Join(apiClone, ".env"), []byte("SECRET=1"), 0644); err != nil {
		t.Fatal(err)
	}
	webClone := originRepo(t) // no origin at all

	old := filepath.Join(home, ".supatree")
	stack := filepath.Join(old, "stacks", "s")
	if err := os.MkdirAll(stack, 0755); err != nil {
		t.Fatal(err)
	}
	run(t, stack, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(stack, SpecName), "members: [api, web]\ndeps:\n  web: [api]\n")
	run(t, stack, "add", "-A")
	run(t, stack, "commit", "-q", "-m", "init")

	writeFile(t, filepath.Join(old, "config.yml"), "version: 1\nstacks:\n  - alias: s\n    path: "+stack+"\n")
	writeFile(t, filepath.Join(old, "events.jsonl"), `{"kind":"approved"}`+"\n")
	writeFile(t, filepath.Join(old, "pm", "AGENTS.md"), "pm")
	writeFile(t, filepath.Join(old, "pm", ".supatree", "mail", "pm", "m.json"), "{}")
	writeFile(t, filepath.Join(old, "layouts", "stale.kdl"), "x")

	wb := &config.Config{Version: 1, Models: map[string]config.Model{
		"claude": {NonoProfile: "claude-code-local", Binary: "claude"},
	}, DefaultModel: "claude", Repos: []config.Repo{
		{Alias: aliasAPI, LocalPath: apiClone, CopyFiles: []string{".env"}},
		{Alias: "web", LocalPath: webClone},
	}}
	data, _ := yaml.Marshal(wb)
	writeFile(t, config.LegacyConfigPath(), string(data))
	return home, apiClone
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func gitGlobal(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"config", "--global"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v %s", err, out)
	}
}

func stubSSHAlias(t *testing.T) {
	t.Helper()
	origResolve, origAlias := resolveRemote, sshAliasOf
	t.Cleanup(func() { resolveRemote, sshAliasOf = origResolve, origAlias })
	resolveRemote = func(raw string) (github.RemoteRef, bool) {
		r, ok := github.ParseRemote(raw)
		if r.Host == "github-work" {
			r.Host = "github.com"
		}
		return r, ok
	}
	sshAliasOf = func(raw string) string {
		if r, ok := github.ParseRemote(raw); ok && r.Host == "github-work" {
			return "github-work"
		}
		return ""
	}
}

func TestMigrate(t *testing.T) {
	home, apiClone := oldLayout(t)
	stubSSHAlias(t)

	var out bytes.Buffer
	if err := Migrate(MigrateOptions{DryRun: true, SkipLiveCheck: true, Out: &out}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(ConfigPath()); err == nil {
		t.Fatal("a dry run wrote the new config")
	}

	out.Reset()
	if err := Migrate(MigrateOptions{SkipLiveCheck: true, Out: &out}); err != nil {
		t.Fatalf("migrate: %v\n%s", err, out.String())
	}

	// The old layout is set aside, not deleted, and is no longer the layout.
	if OldLayout() {
		t.Error("still the old layout after migrating")
	}
	if _, err := os.Stat(filepath.Join(home, ".supatree.pre-xdg", "config.yml")); err != nil {
		t.Error("~/.supatree was not kept as ~/.supatree.pre-xdg")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	stack := cfg.FindStack("s")
	if stack == nil || stack.Path != DefaultStackPath("s") {
		t.Fatalf("stack = %+v, want it moved to %s", stack, DefaultStackPath("s"))
	}
	// Models came across, onto supatree's own profile.
	if m, err := cfg.Model("claude"); err != nil || m.NonoProfile != AgentProfileName {
		t.Errorf("imported claude model = %+v, %v", m, err)
	}

	// The spec names members by canonical URL — never the personal alias.
	spec, err := LoadSpec(stack.Path)
	if err != nil {
		t.Fatalf("new spec: %v", err)
	}
	if got := spec.Members[aliasAPI]; got != "git@github.com:fraud-zero/keystone-api.git" {
		t.Errorf("api URL = %q, want the canonical github.com URL", got)
	}
	if len(spec.Deps["web"]) != 1 {
		t.Errorf("deps lost: %+v", spec.Deps)
	}
	if dirty, _ := hasUncommittedChanges(stack.Path); dirty {
		t.Error("the spec rewrite was not committed")
	}

	// The alias keeps supplying the key for the canonical URL.
	got, _ := exec.CommandContext(context.Background(), "git", "config", "--global", "--get-all",
		"url.git@github-work:fraud-zero/.insteadOf").Output()
	if strings.TrimSpace(string(got)) != "git@github.com:fraud-zero/" {
		t.Errorf("insteadOf = %q", got)
	}

	// Cloned into the cache, pointed at the canonical URL, with its .env.
	clone := filepath.Join(ReposDir(), "github.com", "fraud-zero", "keystone-api")
	if !isClone(clone) {
		t.Fatalf("no cache clone at %s", clone)
	}
	// Stored canonical; get-url would show the insteadOf rewrite to the alias.
	if url, _ := gitOutput(clone, "config", "remote.origin.url"); url != "git@github.com:fraud-zero/keystone-api.git" {
		t.Errorf("cache clone origin = %q", url)
	}
	if url, _ := gitOutput(clone, "remote", "get-url", "origin"); url != "git@github-work:fraud-zero/keystone-api.git" {
		t.Errorf("insteadOf does not route the canonical URL through the alias: %q", url)
	}
	if data, err := os.ReadFile(filepath.Join(clone, ".env")); err != nil || string(data) != "SECRET=1" {
		t.Errorf(".env not carried into the cache clone: %q, %v", data, err)
	}
	if _, ok := cfg.Repos["github.com/fraud-zero/keystone-api"]; !ok {
		t.Errorf("copy_files not recorded: %+v", cfg.Repos)
	}
	// The old clone was only read.
	if url, _ := gitOutput(apiClone, "remote", "get-url", "origin"); url != "git@github-work:fraud-zero/keystone-api.git" {
		t.Errorf("the workbench clone was modified: origin %q", url)
	}

	// State moved; the PM's mailbox is in its home; stale layouts were left.
	if _, err := os.Stat(EventsPath()); err != nil {
		t.Error("ledger not moved")
	}
	if _, err := os.Stat(filepath.Join(PMDir(), "mail", "pm", "m.json")); err != nil {
		t.Error("PM mailbox not moved into its home")
	}
	if _, err := os.Stat(filepath.Join(LayoutsDir(), "stale.kdl")); err == nil {
		t.Error("stale layouts were carried across")
	}

	// And a tree can be made from the migrated stack, with no workbench config.
	if err := os.Remove(config.LegacyConfigPath()); err != nil {
		t.Fatal(err)
	}
	inst, report, err := New(cfg, CreateOptions{Stack: "s", Name: treeLima})
	if err != nil {
		t.Fatalf("new tree after migrating: %v", err)
	}
	if len(inst.Members) != 2 {
		t.Errorf("members = %+v", inst.Members)
	}
	if _, err := os.Stat(filepath.Join(inst.Root, "repos", aliasAPI, ".env")); err != nil {
		t.Errorf("copy_files did not reach the new tree (%v); warnings %v", err, report.Warnings)
	}

	// Running it again is refused rather than half-redone.
	if err := Migrate(MigrateOptions{SkipLiveCheck: true}); err == nil {
		t.Error("a second migrate was not refused")
	}
}

// A tree that still exists, or a stack with uncommitted work, stops the
// migration before anything moves.
func TestMigrateRefuses(t *testing.T) {
	home, _ := oldLayout(t)
	stubSSHAlias(t)
	writeFile(t, filepath.Join(home, ".supatree", "trees", treeLima, ".supatree", "meta.yml"), "name: lima\n")
	writeFile(t, filepath.Join(home, ".supatree", "stacks", "s", "AGENTS.md"), "edited")
	run(t, filepath.Join(home, ".supatree", "stacks", "s"), "add", "AGENTS.md")

	err := Migrate(MigrateOptions{SkipLiveCheck: true})
	if err == nil || !strings.Contains(err.Error(), treeLima) {
		t.Fatalf("err = %v, want a refusal naming the live tree", err)
	}
	if _, statErr := os.Stat(ConfigPath()); statErr == nil {
		t.Error("a refused migration wrote the new config")
	}
	if err := os.RemoveAll(filepath.Join(home, ".supatree", "trees")); err != nil {
		t.Fatal(err)
	}
	err = Migrate(MigrateOptions{SkipLiveCheck: true})
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("err = %v, want a refusal naming the dirty stack", err)
	}
}

package supatree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/setup"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

// Migrating from ~/.supatree to the XDG layout, with supatree owning its
// clones and its stacks naming members by URL.
//
// It is an explicit command rather than something that happens on first run:
// supatree runs unattended in every agent's MCP server, in each sidebar's
// restart loop and in the watcher, and a binary that rearranged files the
// moment it was installed would pull them out from under all of those. It is
// also blunt by design — it refuses while any tree exists — so nothing keyed on
// an old path (a Claude session, a folder-trust entry, a worktree's .git
// pointer) can be left pointing nowhere.

// MigrateOptions parameterizes Migrate.
type MigrateOptions struct {
	DryRun bool // report the plan and change nothing
	// SkipLiveCheck skips the process and session checks. Only for tests,
	// which run beside the developer's own live supatree.
	SkipLiveCheck bool
	Out           io.Writer
}

// sshAliasOf is github.SSHAlias, swappable in tests.
var sshAliasOf = github.SSHAlias

// oldSpec is supatree.yml as it was: members by workbench alias.
type oldSpec struct {
	Members []string            `yaml:"members"`
	Deps    map[string][]string `yaml:"deps,omitempty"`
	Model   string              `yaml:"model,omitempty"`
}

// migrateMember is one stack member's way across.
type migrateMember struct {
	Alias     string
	Local     string // the workbench clone it was resolved through
	URL       string // canonical URL for the new spec
	Key       string // repo cache key
	Alias2SSH string // the ssh config alias the origin went through, if any
	Owner     string
	Host      string
	CopyFiles []string
}

type migrateStack struct {
	Stack   Stack
	NewPath string
	Spec    *oldSpec // nil when already in the URL form
	Members []migrateMember
}

// Migrate moves a ~/.supatree layout across. See the package comment above.
func Migrate(opts MigrateOptions) error {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	if !OldLayout() {
		if _, err := os.Stat(LegacyDir()); err == nil {
			return fmt.Errorf("already migrated: %s exists; %s is a leftover", ConfigPath(), LegacyDir())
		}
		return fmt.Errorf("nothing to migrate: there is no %s", LegacyDir())
	}
	old := LegacyDir()

	oldCfg, err := loadOldConfig(old)
	if err != nil {
		return err
	}
	if err := refuseLive(old, oldCfg, opts.SkipLiveCheck); err != nil {
		return err
	}
	wb := loadWorkbenchConfig()

	stacks, err := planStacks(old, oldCfg, wb)
	if err != nil {
		return err
	}
	cfg := newConfigFrom(oldCfg, wb, out)
	for _, st := range stacks {
		for _, m := range st.Members {
			if len(m.CopyFiles) > 0 {
				if cfg.Repos == nil {
					cfg.Repos = map[string]RepoSettings{}
				}
				cfg.Repos[m.Key] = RepoSettings{CopyFiles: m.CopyFiles}
			}
		}
	}

	printPlan(out, old, stacks)
	if opts.DryRun {
		_, _ = fmt.Fprintln(out, "\n(dry run — nothing changed)")
		return nil
	}

	// Under the old registry lock: nothing should be running, but a stray CLI
	// that started after the checks must wait rather than interleave.
	return config.WithFileLock(filepath.Join(old, "config.yml.lock"), func() error {
		if err := EnsureLayout(); err != nil {
			return err
		}
		if err := moveFiles(old); err != nil {
			return err
		}
		for i := range stacks {
			if err := migrateRepos(stacks[i].Members, out); err != nil {
				return err
			}
		}
		for i := range stacks {
			if err := migrateStackRepo(&stacks[i], out); err != nil {
				return err
			}
		}
		cfg.Stacks = cfg.Stacks[:0]
		for _, st := range stacks {
			s := st.Stack
			s.Path = st.NewPath
			cfg.Stacks = append(cfg.Stacks, s)
		}
		// Last but one: the new config's existence is what ends the old
		// layout, so everything before it can be re-run after a failure.
		if err := cfg.Save(); err != nil {
			return err
		}
		if path, err := AgentProfile().Write(); err != nil {
			_, _ = fmt.Fprintf(out, "warning: could not write the nono profile (%v) — run: supatree init\n", err)
		} else {
			_, _ = fmt.Fprintf(out, "wrote nono profile %s\n", path)
		}
		backup := old + ".pre-xdg"
		if _, err := os.Stat(backup); err == nil {
			backup += "." + time.Now().Format("20060102-150405")
		}
		if err := os.Rename(old, backup); err != nil {
			return fmt.Errorf("migrated, but could not set %s aside: %w", old, err)
		}
		_, _ = fmt.Fprintf(out, "\nmigrated. The old layout is at %s — delete it by hand once you are happy.\n", backup)
		return nil
	})
}

func loadOldConfig(old string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(old, "config.yml"))
	if os.IsNotExist(err) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s/config.yml: %w", old, err)
	}
	return &c, nil
}

// loadWorkbenchConfig reads workbench's config — XDG first, then the pre-XDG
// file — read-only. Nil when there is none.
func loadWorkbenchConfig() *config.Config {
	for _, path := range []string{config.ConfigPath(), config.LegacyConfigPath()} {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if wb, err := config.LoadFile(path); err == nil {
			return wb
		}
	}
	return nil
}

// refuseLive refuses while anything could still be using the old layout. "No
// trees" alone is not enough: a tree's agents, its sidebar loop or an
// orphaned PM each hold an old-binary process that would keep writing old
// paths after the move.
func refuseLive(old string, oldCfg *Config, skip bool) error {
	var problems []string
	oldBase := oldCfg.TreesBase
	if oldBase == "" {
		oldBase = filepath.Join(old, "trees")
	}
	if entries, err := os.ReadDir(oldBase); err == nil {
		for _, e := range entries {
			if _, err := os.Stat(filepath.Join(oldBase, e.Name(), stateDirName, "meta.yml")); err == nil {
				problems = append(problems, "supatree "+e.Name()+" still exists — remove it: supatree rm "+e.Name())
			}
		}
	}
	if !skip {
		err := config.TryFileLock(filepath.Join(old, "watch.lock"), func() error { return nil })
		if errors.Is(err, config.ErrLockBusy) {
			problems = append(problems, "the watcher is running (it holds "+filepath.Join(old, "watch.lock")+")")
		}
		if sessions, err := zellij.ListSessions(); err == nil {
			for _, s := range sessions {
				if !s.Exited && strings.HasPrefix(s.Name, "st-") {
					problems = append(problems, "zellij session "+s.Name+" is running — quit it")
				}
			}
		}
		if procs, err := setup.OtherProcesses("supatree"); err == nil {
			for _, p := range procs {
				problems = append(problems, "process still running: "+p.String())
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("not migrating while supatree is in use:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// planStacks works out, before anything changes, how every registered stack
// and every member gets across — and refuses if any of it cannot.
func planStacks(old string, oldCfg *Config, wb *config.Config) ([]migrateStack, error) {
	oldStacks := filepath.Join(old, "stacks")
	var plans []migrateStack
	var problems []string
	for _, s := range oldCfg.Stacks {
		st := migrateStack{Stack: s, NewPath: s.Path}
		if underDir(oldStacks, s.Path) {
			rel, _ := filepath.Rel(oldStacks, s.Path)
			st.NewPath = filepath.Join(StacksDir(), rel)
			_, oldErr := os.Stat(s.Path)
			_, newErr := os.Stat(st.NewPath)
			switch {
			case oldErr != nil && newErr == nil:
				// Moved by an earlier run that failed later on: carry on
				// from where it is now.
				st.Stack.Path = st.NewPath
			case newErr == nil:
				problems = append(problems, fmt.Sprintf("stack %s: %s already exists", s.Alias, st.NewPath))
				continue
			}
		}
		s = st.Stack
		if err := git.RequireSafeRepo(s.Path); err != nil {
			problems = append(problems, fmt.Sprintf("stack %s: %v", s.Alias, err))
			continue
		}
		if dirty, err := hasUncommittedChanges(s.Path); err != nil {
			problems = append(problems, fmt.Sprintf("stack %s at %s: %v", s.Alias, s.Path, err))
			continue
		} else if dirty {
			problems = append(problems, fmt.Sprintf("stack %s at %s has uncommitted changes — commit or stash them first", s.Alias, s.Path))
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Path, SpecName))
		if err != nil {
			problems = append(problems, fmt.Sprintf("stack %s: %v", s.Alias, err))
			continue
		}
		if spec, err := parseSpec(data); err == nil {
			// Already in the URL form — rewritten by an earlier run. Its
			// members still carry copy_files across from workbench.
			for _, alias := range spec.Aliases() {
				if m, ok := planConverted(alias, spec.Members[alias], wb); ok {
					st.Members = append(st.Members, m)
				}
			}
			plans = append(plans, st)
			continue
		}
		var spec oldSpec
		if err := yaml.Unmarshal(data, &spec); err != nil {
			problems = append(problems, fmt.Sprintf("stack %s: parse %s: %v", s.Alias, SpecName, err))
			continue
		}
		st.Spec = &spec
		for _, alias := range spec.Members {
			m, err := planMember(alias, wb)
			if err != nil {
				problems = append(problems, fmt.Sprintf("stack %s: %v", s.Alias, err))
				continue
			}
			st.Members = append(st.Members, m)
		}
		plans = append(plans, st)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("cannot migrate yet:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return plans, nil
}

// planMember resolves an old alias through workbench's config to the clone it
// named, and derives the URL the new spec carries: the canonical one. A
// personal ssh alias in the origin is resolved to its real host — the spec is
// shared, and an alias only means something on the machine that defines it —
// and remembered, so the alias can keep supplying the key (insteadOf).
func planMember(alias string, wb *config.Config) (migrateMember, error) {
	if wb == nil {
		return migrateMember{}, fmt.Errorf("member %q: no workbench config to resolve it through", alias)
	}
	repo, _ := wb.FindRepo(alias)
	if repo == nil {
		return migrateMember{}, fmt.Errorf("member %q is not a repo in workbench's config", alias)
	}
	m := migrateMember{Alias: alias, Local: repo.LocalPath, CopyFiles: repo.CopyFiles}
	origin, err := gitOutput(repo.LocalPath, "remote", "get-url", "origin")
	if err != nil || origin == "" {
		// A repository with no remote: the clone itself is the source.
		m.URL = repo.LocalPath
	} else if r, ok := resolveRemote(origin); ok {
		m.URL = origin
		if isSSHForm(origin) {
			m.URL = r.SSHURL()
			m.Alias2SSH = sshAliasOf(origin)
			m.Owner, m.Host = r.Owner, r.Host
		}
	} else {
		m.URL = origin
	}
	key, err := CacheKey(m.URL)
	if err != nil {
		return migrateMember{}, fmt.Errorf("member %q: %w", alias, err)
	}
	m.Key = key
	return m, nil
}

// planConverted is planMember for a member already in the URL form: the clone
// workbench knew it by, if it has one, supplies the cache seed and copy_files.
func planConverted(alias, url string, wb *config.Config) (migrateMember, bool) {
	if wb == nil {
		return migrateMember{}, false
	}
	repo, _ := wb.FindRepo(alias)
	key, err := CacheKey(url)
	if repo == nil || err != nil {
		return migrateMember{}, false
	}
	return migrateMember{Alias: alias, Local: repo.LocalPath, URL: url, Key: key, CopyFiles: repo.CopyFiles}, true
}

func isSSHForm(url string) bool {
	return strings.HasPrefix(url, "ssh://") || (strings.Contains(url, ":") && !strings.Contains(url, "://") && !filepath.IsAbs(url))
}

// newConfigFrom builds the new config from the old one, importing — once, read
// only — the models workbench's config defined.
func newConfigFrom(oldCfg *Config, wb *config.Config, out io.Writer) *Config {
	c := *oldCfg
	c.Version = 1
	if c.TreesBase == filepath.Join(LegacyDir(), "trees") {
		c.TreesBase = ""
	}
	if len(c.Models) == 0 {
		if wb != nil && len(wb.Models) > 0 {
			c.Models = make(map[string]config.Model, len(wb.Models))
			for k, m := range wb.Models {
				// workbench's generated profile is workbench's; supatree's
				// agents run under their own.
				if m.NonoProfile == "claude-code-local" || m.NonoProfile == "claude-code" {
					m.NonoProfile = AgentProfileName
				}
				c.Models[k] = m
			}
			if c.DefaultModel == "" {
				c.DefaultModel = wb.DefaultModel
			}
			_, _ = fmt.Fprintf(out, "imported %d model(s) from workbench's config\n", len(c.Models))
		} else {
			c.Models = DefaultModels()
		}
	}
	return &c
}

func printPlan(out io.Writer, old string, stacks []migrateStack) {
	_, _ = fmt.Fprintf(out, "moving %s to:\n  config  %s\n  state   %s\n  cache   %s\n  repos   %s\n",
		old, ConfigDir(), StateRoot(), CacheDir(), ReposDir())
	for _, st := range stacks {
		_, _ = fmt.Fprintf(out, "\nstack %s (%s", st.Stack.Alias, st.Stack.Path)
		if st.NewPath != st.Stack.Path {
			_, _ = fmt.Fprintf(out, " → %s", st.NewPath)
		}
		_, _ = fmt.Fprintln(out, ")")
		if st.Spec == nil {
			_, _ = fmt.Fprintln(out, "  supatree.yml already names members by URL")
			continue
		}
		for _, m := range st.Members {
			_, _ = fmt.Fprintf(out, "  %-20s %s\n", m.Alias, m.URL)
			if m.Alias2SSH != "" {
				_, _ = fmt.Fprintf(out, "  %-20s git config --global --add %s %s\n", "", insteadOfKey(m), insteadOfValue(m))
			}
		}
	}
}

func insteadOfKey(m migrateMember) string {
	return "url.git@" + m.Alias2SSH + ":" + m.Owner + "/.insteadOf"
}

func insteadOfValue(m migrateMember) string {
	return "git@" + m.Host + ":" + m.Owner + "/"
}

// moveFiles copies everything of the old layout to its XDG home, except the
// layouts, which are regenerated on demand and mostly stale.
func moveFiles(old string) error {
	moves := []struct{ from, to string }{
		{"schedule.yml", filepath.Join(ConfigDir(), "schedule.yml")},
		{"ui.yml", UIStatePath()},
		{"events.jsonl", EventsPath()},
		{"events.jsonl.1", EventsPath() + ".1"},
		{"requests.jsonl", RequestsPath()},
		{"notify.jsonl", NotifyPath()},
		{"archive", filepath.Join(StateRoot(), "archive")},
		{"logs", LogsDir()},
		{"cache/pr-status.json", PRCachePath()},
		{"cache/pr-comments.json", CommentsCachePath()},
		{"cache/recall.jsonl", RecallLogPath()},
		{"cache/last-status.json", LastSummaryPath()},
		{"cache/notify-state.json", NotifyStatePath()},
		{"cache/schedule-state.json", ScheduleStatePath()},
	}
	for _, m := range moves {
		if err := config.CopyPath(filepath.Join(old, m.from), m.to); err != nil {
			return fmt.Errorf("copy %s: %w", m.from, err)
		}
	}
	// The PM's home, whose mailbox used to sit in pm/.supatree/ the way every
	// tree's did; its home is its state dir now.
	oldPM := filepath.Join(old, "pm")
	entries, err := os.ReadDir(oldPM)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if e.Name() == stateDirName {
			if err := config.CopyPath(filepath.Join(oldPM, e.Name()), PMDir()); err != nil {
				return err
			}
			continue
		}
		if err := config.CopyPath(filepath.Join(oldPM, e.Name()), filepath.Join(PMDir(), e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// migrateRepos puts each member's repository in the cache: cloned from the
// clone workbench knew it by — fast, and offline-safe — then pointed at the
// canonical URL and fetched, so it is a clone of the real remote from then on.
func migrateRepos(members []migrateMember, out io.Writer) error {
	for _, m := range members {
		if m.Alias2SSH != "" {
			if err := ensureInsteadOf(m, out); err != nil {
				return err
			}
		}
		dest := filepath.Join(ReposDir(), m.Key)
		if !isClone(dest) {
			if err := cloneInto(m.Local, dest); err != nil {
				return fmt.Errorf("clone %s into the cache: %w", m.Local, err)
			}
			if m.URL != m.Local {
				if err := runGit(dest, "remote", "set-url", "origin", m.URL); err != nil {
					return err
				}
				fetchOrigin(dest, out)
			}
			_, _ = fmt.Fprintf(out, "cloned %s\n", m.Key)
		}
		missing, err := config.CopyFiles(m.Local, dest, m.CopyFiles)
		if err != nil {
			return fmt.Errorf("copy_files for %s: %w", m.Alias, err)
		}
		for _, f := range missing {
			_, _ = fmt.Fprintf(out, "warning: %s: copy_files: %s not found in %s\n", m.Alias, f, m.Local)
		}
	}
	return nil
}

// fetchOrigin refreshes a fresh cache clone from its real remote. Best effort:
// offline, the clone still holds what the local one had.
func fetchOrigin(dir string, out io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "fetch", "--quiet", "origin")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if o, err := cmd.CombinedOutput(); err != nil {
		_, _ = fmt.Fprintf(out, "warning: fetch %s: %v %s (it still has what the old clone had)\n", dir, err, strings.TrimSpace(string(o)))
		return
	}
	_ = runGit(dir, "remote", "set-head", "origin", "--auto")
}

// ensureInsteadOf makes the canonical URL authenticate the way the alias did:
// git rewrites git@<host>:<owner>/ to the alias, whose ssh config picks the key.
func ensureInsteadOf(m migrateMember, out io.Writer) error {
	key := insteadOfKey(m)
	have, _ := exec.CommandContext(context.Background(), "git", "config", "--global", "--get-all", key).Output()
	for _, line := range strings.Split(string(have), "\n") {
		if strings.TrimSpace(line) == insteadOfValue(m) {
			return nil
		}
	}
	_, _ = fmt.Fprintf(out, "git config --global --add %s %s\n", key, insteadOfValue(m))
	if o, err := exec.CommandContext(context.Background(), "git", "config", "--global", "--add", key, insteadOfValue(m)).CombinedOutput(); err != nil {
		return fmt.Errorf("git config %s: %w: %s", key, err, strings.TrimSpace(string(o)))
	}
	return nil
}

// migrateStackRepo rewrites a stack's supatree.yml to the URL form, as a
// commit, and moves the stack out of the old layout if it lived there.
func migrateStackRepo(st *migrateStack, out io.Writer) error {
	if st.Spec != nil {
		spec := &Spec{Members: map[string]string{}, Deps: st.Spec.Deps, Model: st.Spec.Model}
		for _, m := range st.Members {
			spec.Members[m.Alias] = m.URL
		}
		if err := SaveSpec(st.Stack.Path, spec); err != nil {
			return err
		}
		if err := runGit(st.Stack.Path, "add", SpecName); err != nil {
			return err
		}
		if err := runGit(st.Stack.Path, "commit", "-q", "-m", "supatree: members carry their git URL"); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "rewrote %s in stack %s\n", SpecName, st.Stack.Alias)
	}
	if st.NewPath != st.Stack.Path {
		if err := os.MkdirAll(filepath.Dir(st.NewPath), 0755); err != nil {
			return err
		}
		if err := os.Rename(st.Stack.Path, st.NewPath); err != nil {
			return fmt.Errorf("move stack %s: %w", st.Stack.Alias, err)
		}
		_, _ = fmt.Fprintf(out, "moved stack %s to %s\n", st.Stack.Alias, st.NewPath)
	}
	// No tree survives a migration, so any worktree the stack still records
	// is a leftover pointing at the old layout.
	_ = runGit(st.NewPath, "worktree", "prune")
	return nil
}

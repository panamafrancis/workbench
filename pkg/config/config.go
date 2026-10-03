package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version             int              `yaml:"version"`
	DefaultModel        string           `yaml:"default_model"`
	WorktreeBase        string           `yaml:"worktree_base"`
	DefaultZellijLayout string           `yaml:"default_zellij_layout"`
	SidebarWidth        string           `yaml:"sidebar_width"`
	DisableUpdateCheck  bool             `yaml:"update_check_disabled"`
	ShowStats           *bool            `yaml:"show_stats,omitempty"`
	Models              map[string]Model `yaml:"models"`
	Repos               []Repo           `yaml:"repos"`
}

func (c *Config) UpdateCheck() bool {
	return !c.DisableUpdateCheck
}

func (c *Config) ResolveShowStats() bool {
	if c.ShowStats != nil {
		return *c.ShowStats
	}
	return true
}

type Model struct {
	NonoProfile string   `yaml:"nono_profile"`
	Binary      string   `yaml:"binary"`
	Args        []string `yaml:"args"`
	// ResumeArgs are appended to Args only when a prior session exists for the
	// worktree being opened (e.g. claude's "--continue"). Empty for models that
	// have no resume concept.
	ResumeArgs []string `yaml:"resume_args"`
	// NewSessionArgs and ResumeSessionArgs support pinning a launch to a specific
	// session ID so several named agents can share one directory yet resume
	// independently (used by supatree). Each occurrence of the literal
	// "{session_id}" is substituted with the agent's generated ID. Empty for
	// models whose CLI has no explicit session-ID flag; supatree then falls back
	// to a single directory-resumed agent via ResumeArgs.
	NewSessionArgs    []string `yaml:"new_session_args,omitempty"`
	ResumeSessionArgs []string `yaml:"resume_session_args,omitempty"`
	// AgentNameArgs give the agent a stable name on whatever cross-session
	// message bus its CLI provides, so siblings can address it. Each occurrence
	// of "{agent_name}" is substituted. Empty for models with no such bus, which
	// then fall back to file mailboxes alone.
	AgentNameArgs []string `yaml:"agent_name_args,omitempty"`
	// PromptArgs hand the agent its first message at launch, so it starts
	// working instead of waiting at an empty prompt. Each occurrence of
	// "{prompt}" is substituted. Empty for models with no such argument, which
	// then start idle and pick their instructions up on the first human turn.
	PromptArgs []string `yaml:"prompt_args,omitempty"`
}

type Repo struct {
	Alias     string     `yaml:"alias"`
	LocalPath string     `yaml:"local_path"`
	CopyFiles []string   `yaml:"copy_files,omitempty"`
	Worktrees []Worktree `yaml:"worktrees"`
}

type Worktree struct {
	Name      string    `yaml:"name"`
	Branch    string    `yaml:"branch"`
	Path      string    `yaml:"path"`
	CreatedAt time.Time `yaml:"created_at"`
	Model     string    `yaml:"model"`
}

func DefaultConfig() *Config {
	return &Config{
		Version:      1,
		DefaultModel: "claude",
		Models:       DefaultModels(),
		Repos:        []Repo{},
	}
}

// DefaultModels returns the built-in model entries, as a fresh map the caller
// may modify. Both tools seed their own config from it, so the definitions live
// in one place even though nothing on disk is shared.
func DefaultModels() map[string]Model {
	return map[string]Model{
		"claude": {
			NonoProfile:       "claude-code",
			Binary:            "claude",
			Args:              []string{"--dangerously-skip-permissions"},
			ResumeArgs:        []string{"--continue"},
			NewSessionArgs:    []string{"--session-id", "{session_id}"},
			ResumeSessionArgs: []string{"--resume", "{session_id}"},
			AgentNameArgs:     []string{"--name", "{agent_name}"},
			PromptArgs:        []string{"{prompt}"},
		},
		"codex": {
			NonoProfile: "default",
			Binary:      "codex",
			Args:        []string{},
		},
		"opencode": {
			NonoProfile: "default",
			Binary:      "opencode",
			Args:        []string{},
		},
		"dirac": {
			NonoProfile: "default",
			Binary:      "dirac",
			Args:        []string{},
		},
		"shell": {
			NonoProfile: "default",
			Binary:      "bash",
			Args:        []string{},
		},
	}
}

func Load() (*Config, error) {
	return LoadFile(ConfigPath())
}

// LoadFile reads a workbench config from path, returning defaults when the
// file is absent.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Models == nil {
		cfg.Models = DefaultModels()
	}
	BackfillModels(cfg.Models)
	return &cfg, nil
}

// BackfillModels fills in new_session_args/resume_session_args for models
// that still match a shipped default (same key and binary) but predate those
// fields. This lets multi-agent tools (supatree) resume distinct sessions in a
// shared directory without requiring users to hand-edit an existing config. It
// only adds capability — it never overwrites args the user already set — and is
// invisible to workbench, which does not read these fields.
func BackfillModels(models map[string]Model) {
	for key, dm := range DefaultModels() {
		if len(dm.NewSessionArgs) == 0 && len(dm.ResumeSessionArgs) == 0 {
			continue
		}
		m, ok := models[key]
		if !ok || m.Binary != dm.Binary {
			continue
		}
		if len(m.NewSessionArgs) == 0 && len(m.ResumeSessionArgs) == 0 {
			m.NewSessionArgs = dm.NewSessionArgs
			m.ResumeSessionArgs = dm.ResumeSessionArgs
			models[key] = m
		}
	}
	// PromptArgs arrived later than the session args, so a config that already
	// had those backfilled still lacks it. Same rule: shipped key and binary,
	// never overwriting.
	for key, dm := range DefaultModels() {
		m, ok := models[key]
		if !ok || m.Binary != dm.Binary || len(dm.PromptArgs) == 0 || len(m.PromptArgs) > 0 {
			continue
		}
		m.PromptArgs = dm.PromptArgs
		models[key] = m
	}
}

func (c *Config) Save() error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return os.Rename(tmp, path)
}

// AddWorktree reloads the config from disk, appends wt to the repo identified
// by alias, and saves. Reading fresh from disk (instead of mutating a possibly
// stale in-memory snapshot and writing the whole file back) prevents a caller
// from resurrecting worktrees that were deleted concurrently by another process
// or TUI instance. A worktree whose name already exists is left untouched.
func AddWorktree(alias string, wt Worktree) error {
	return withConfigLock(func() error {
		cfg, err := Load()
		if err != nil {
			return err
		}
		repo, idx := cfg.FindRepo(alias)
		if repo == nil {
			return fmt.Errorf("repo %q not found", alias)
		}
		for _, w := range cfg.Repos[idx].Worktrees {
			if w.Name == wt.Name {
				return nil
			}
		}
		cfg.Repos[idx].Worktrees = append(cfg.Repos[idx].Worktrees, wt)
		return cfg.Save()
	})
}

// RemoveWorktreeEntry reloads the config from disk, removes the worktree with
// the given name, and saves. Like AddWorktree it works against current disk
// state so it never clobbers concurrent changes to other worktrees.
func RemoveWorktreeEntry(name string) error {
	return withConfigLock(func() error {
		cfg, err := Load()
		if err != nil {
			return err
		}
		for ri := range cfg.Repos {
			for wi := range cfg.Repos[ri].Worktrees {
				if cfg.Repos[ri].Worktrees[wi].Name == name {
					cfg.Repos[ri].Worktrees = slices.Delete(cfg.Repos[ri].Worktrees, wi, wi+1)
					return cfg.Save()
				}
			}
		}
		return nil
	})
}

func (c *Config) ResolveWorktreeBase() string {
	if c.WorktreeBase != "" {
		return c.WorktreeBase
	}
	return DefaultWorktreeBase()
}

func (c *Config) FindRepo(alias string) (*Repo, int) {
	for i := range c.Repos {
		if c.Repos[i].Alias == alias {
			return &c.Repos[i], i
		}
	}
	return nil, -1
}

func (c *Config) FindWorktree(name string) (*Worktree, *Repo) {
	for ri := range c.Repos {
		for wi := range c.Repos[ri].Worktrees {
			if c.Repos[ri].Worktrees[wi].Name == name {
				return &c.Repos[ri].Worktrees[wi], &c.Repos[ri]
			}
		}
	}
	return nil, nil
}

func (c *Config) FindWorktreeByPath(path string) (*Worktree, *Repo) {
	path = filepath.Clean(path)
	for ri := range c.Repos {
		for wi := range c.Repos[ri].Worktrees {
			if filepath.Clean(c.Repos[ri].Worktrees[wi].Path) == path {
				return &c.Repos[ri].Worktrees[wi], &c.Repos[ri]
			}
		}
	}
	return nil, nil
}

func (c *Config) AllWorktreeNames() []string {
	var names []string
	for _, r := range c.Repos {
		for _, w := range r.Worktrees {
			names = append(names, w.Name)
		}
	}
	return names
}

// WorktreeNameSet returns the set of all worktree names across every repo,
// convenient for membership checks (e.g. reserved-city reclaim).
func (c *Config) WorktreeNameSet() map[string]bool {
	names := c.AllWorktreeNames()
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

func (c *Config) ResolveSidebarWidth() string {
	if c.SidebarWidth != "" {
		return c.SidebarWidth
	}
	return "20%"
}

func (c *Config) ResolveModel(model string) string {
	if model != "" {
		return model
	}
	if c.DefaultModel != "" {
		return c.DefaultModel
	}
	return "claude"
}

// Model returns the models entry for key, or an error naming the config
// section to add it to.
func (c *Config) Model(key string) (Model, error) {
	m, ok := c.Models[key]
	if !ok {
		return Model{}, fmt.Errorf("unknown model %q (add it under 'models:' in config)", key)
	}
	return m, nil
}

// CopyFiles copies each pattern (a file or directory relative to srcDir) into
// the same relative place under dstDir. It is how gitignored local files —
// credentials in a .env, mostly — reach a fresh worktree from the checkout
// they were put in by hand.
//
// A pattern whose source does not exist is returned in missing rather than
// failing the copy: a worktree without its .env is still a worktree, and the
// caller says so. A pattern that is absolute or escapes srcDir is an error.
func CopyFiles(srcDir, dstDir string, patterns []string) (missing []string, err error) {
	root := filepath.Clean(srcDir) + string(filepath.Separator)
	for _, pattern := range patterns {
		if filepath.IsAbs(pattern) {
			return missing, fmt.Errorf("copy_files: absolute paths not allowed: %s", pattern)
		}
		src := filepath.Clean(filepath.Join(srcDir, pattern))
		if !strings.HasPrefix(src+string(filepath.Separator), root) && src != filepath.Clean(srcDir) {
			return missing, fmt.Errorf("copy_files: path escapes repo root: %s", pattern)
		}
		dst := filepath.Join(dstDir, pattern)

		info, statErr := os.Stat(src)
		if os.IsNotExist(statErr) {
			missing = append(missing, pattern)
			continue
		}
		if statErr != nil {
			return missing, fmt.Errorf("copy_files: %s: %w", pattern, statErr)
		}
		if info.IsDir() {
			if err := copyDir(src, dst); err != nil {
				return missing, fmt.Errorf("copy_files: %s: %w", pattern, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return missing, fmt.Errorf("copy_files: %s: %w", pattern, err)
		}
		if err := copyFile(src, dst, info.Mode()); err != nil {
			return missing, fmt.Errorf("copy_files: %s: %w", pattern, err)
		}
	}
	return missing, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

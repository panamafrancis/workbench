package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
}

type Repo struct {
	Alias               string     `yaml:"alias"`
	LocalPath           string     `yaml:"local_path"`
	StartupScript       string     `yaml:"startup_script"`
	CleanupScript       string     `yaml:"cleanup_script"`
	StartupInstructions string     `yaml:"startup_instructions"`
	Worktrees           []Worktree `yaml:"worktrees"`
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
		Models: map[string]Model{
			"claude": {
				NonoProfile: "claude-code",
				Binary:      "claude",
				Args:        []string{"--dangerously-skip-permissions"},
				ResumeArgs:  []string{"--continue"},
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
		},
		Repos: []Repo{},
	}
}

func Load() (*Config, error) {
	path := ConfigPath()
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
		cfg.Models = DefaultConfig().Models
	}
	return &cfg, nil
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

func (c *Config) AllWorktreeNames() []string {
	var names []string
	for _, r := range c.Repos {
		for _, w := range r.Worktrees {
			names = append(names, w.Name)
		}
	}
	return names
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

func (r *Repo) RunStartup(worktreePath, worktreeName string) error {
	if r.StartupScript == "" {
		return nil
	}
	return runScript(r.StartupScript, worktreePath, worktreeName)
}

func (r *Repo) RunCleanup(worktreePath, worktreeName string) error {
	if r.CleanupScript == "" {
		return nil
	}
	return runScript(r.CleanupScript, worktreePath, worktreeName)
}

func runScript(script, worktreePath, worktreeName string) error {
	script = filepath.Clean(script)
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("script not found: %s", script)
	}
	cmd := exec.CommandContext(context.Background(), "bash", "--", script)
	cmd.Env = append(os.Environ(),
		"WORKBENCH_WORKTREE_PATH="+worktreePath,
		"WORKBENCH_WORKTREE_NAME="+worktreeName,
	)
	cmd.Dir = worktreePath
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			return fmt.Errorf("%s: %s", script, msg)
		}
		return fmt.Errorf("%s: %w", script, err)
	}
	return nil
}

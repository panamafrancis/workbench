package supatree

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/panamafrancis/workbench/pkg/config"
)

// Config is the supatree registry (~/.supatree/config.yml). It lists the
// registered stack repos and defaults. Live supatrees are NOT stored here — they
// are discovered by scanning the trees base for per-tree metadata.
type Config struct {
	Version      int     `yaml:"version"`
	DefaultModel string  `yaml:"default_model,omitempty"`
	SidebarWidth string  `yaml:"sidebar_width,omitempty"`
	TreesBase    string  `yaml:"trees_base,omitempty"`
	Stacks       []Stack `yaml:"stacks"`
}

// Stack is a registered stack repo.
type Stack struct {
	Alias string `yaml:"alias"`
	Path  string `yaml:"path"`
}

// DefaultConfig returns the config used when none exists on disk.
func DefaultConfig() *Config {
	return &Config{Version: 1, Stacks: []Stack{}}
}

// Load reads the registry, returning defaults when the file is absent.
func Load() (*Config, error) {
	data, err := os.ReadFile(ConfigPath())
	if os.IsNotExist(err) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read supatree config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse supatree config: %w", err)
	}
	return &c, nil
}

// Save writes the registry atomically (temp + rename).
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0755); err != nil {
		return fmt.Errorf("create supatree dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal supatree config: %w", err)
	}
	tmp := ConfigPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write supatree config: %w", err)
	}
	return os.Rename(tmp, ConfigPath())
}

// FindStack returns the registered stack with the given alias, or nil.
func (c *Config) FindStack(alias string) *Stack {
	for i := range c.Stacks {
		if c.Stacks[i].Alias == alias {
			return &c.Stacks[i]
		}
	}
	return nil
}

// ResolveTreesBase returns the configured trees base or the default.
func (c *Config) ResolveTreesBase() string {
	if c.TreesBase != "" {
		return c.TreesBase
	}
	return DefaultTreesBase()
}

// ResolveModel returns the effective model key: explicit override, else the
// registry default, else workbench's default.
func (c *Config) ResolveModel(override string, wb *config.Config) string {
	if override != "" {
		return override
	}
	if c.DefaultModel != "" {
		return c.DefaultModel
	}
	return wb.ResolveModel("")
}

// ResolveSidebarWidth returns the sidebar width or a default.
func (c *Config) ResolveSidebarWidth() string {
	if c.SidebarWidth != "" {
		return c.SidebarWidth
	}
	return "25%"
}

// AddStack registers a stack repo under the registry lock. It is idempotent on
// alias: an existing alias with the same path is left unchanged; a different
// path is an error.
func AddStack(alias, path string) error {
	return config.WithFileLock(LockPath(), func() error {
		c, err := Load()
		if err != nil {
			return err
		}
		if s := c.FindStack(alias); s != nil {
			if filepath.Clean(s.Path) != filepath.Clean(path) {
				return fmt.Errorf("stack %q already registered at %s", alias, s.Path)
			}
			return nil
		}
		c.Stacks = append(c.Stacks, Stack{Alias: alias, Path: filepath.Clean(path)})
		return c.Save()
	})
}

// RemoveStack unregisters a stack repo under the registry lock.
func RemoveStack(alias string) error {
	return config.WithFileLock(LockPath(), func() error {
		c, err := Load()
		if err != nil {
			return err
		}
		out := c.Stacks[:0]
		for _, s := range c.Stacks {
			if s.Alias != alias {
				out = append(out, s)
			}
		}
		c.Stacks = out
		return c.Save()
	})
}

package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/panamafrancis/workbench/pkg/config"
)

// Config is the supatree registry (~/.supatree/config.yml). It lists the
// registered stack repos and defaults. Live supatrees are NOT stored here — they
// are discovered by scanning the trees base for per-tree metadata.
type Config struct {
	Version      int    `yaml:"version"`
	DefaultModel string `yaml:"default_model,omitempty"`
	SidebarWidth string `yaml:"sidebar_width,omitempty"`
	// PMModelKey is the models entry the PM agent runs under. It is separate
	// from DefaultModel so the PM can point at a different nono profile: it
	// executes no third-party code but reads text other people wrote and holds
	// credentials that reach outside the machine, so it wants a profile that is
	// narrow on egress rather than one that is wide on the filesystem.
	PMModelKey string  `yaml:"pm_model,omitempty"`
	TreesBase  string  `yaml:"trees_base,omitempty"`
	Stacks     []Stack `yaml:"stacks"`
	// NotifyCommand is the argv `supatree watch` runs to deliver a desktop
	// notification, with {title} and {text} substituted. Empty uses the macOS
	// default; Linux points it at notify-send without a code change.
	NotifyCommand []string `yaml:"notify_command,omitempty"`
	// WatchInterval is how often the watcher re-derives status. Empty uses
	// DefaultWatchInterval. The GitHub fetch behind it stays gated on
	// PRStaleAge, so shortening this does not spend more quota.
	WatchInterval string `yaml:"watch_interval,omitempty"`
	// DefaultAutonomy governs supatrees whose meta.yml says nothing, and — the
	// reason it has to exist — actions with no tree to carry a level yet, such
	// as creating one.
	DefaultAutonomy string `yaml:"default_autonomy,omitempty"`
	// DefaultOutward allows actions a third party sees. Off unless you say
	// otherwise, at every autonomy level.
	DefaultOutward bool `yaml:"default_outward,omitempty"`
	// ReviewOutward is the outward default for review trees, and it is on
	// unless set false. Posting the review is what a review tree is for: a
	// tree you made to review someone's pull request, whose every other
	// authoring verb is refused, has no second use for the permission to
	// withhold. A tree's own `outward` still overrides it.
	ReviewOutward *bool `yaml:"review_outward,omitempty"`
	// Models is supatree's own open map of launchable models — the same shape
	// as workbench's, never read from it. Empty means the built-in defaults.
	Models map[string]config.Model `yaml:"models,omitempty"`
	// Repos holds personal per-repo settings, keyed by host/owner/repo. They
	// are local to this machine and never belong in a shared supatree.yml.
	Repos map[string]RepoSettings `yaml:"repos,omitempty"`

	// legacy is workbench's config, read only while supatree's own config
	// does not yet carry what used to come from it (see legacy.go).
	legacy *config.Config
}

// RepoSettings are one repo's personal, machine-local settings.
type RepoSettings struct {
	// CopyFiles lists gitignored files (a .env, mostly) copied from the base
	// clone's checkout into every new member worktree of this repo.
	CopyFiles []string `yaml:"copy_files,omitempty"`
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
		c := DefaultConfig()
		c.legacy = loadLegacy()
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read supatree config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse supatree config: %w", err)
	}
	if c.Models != nil {
		config.BackfillModels(c.Models)
	}
	c.legacy = loadLegacy()
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
// registry default, else "claude".
func (c *Config) ResolveModel(override string) string {
	if override != "" {
		return override
	}
	if c.DefaultModel != "" {
		return c.DefaultModel
	}
	if wb := c.legacyModels(); wb != nil && wb.DefaultModel != "" {
		return wb.DefaultModel
	}
	return defaultModelKey
}

// defaultModelKey is the model used when nothing names one.
const defaultModelKey = "claude"

// Model returns the models entry for key.
func (c *Config) Model(key string) (config.Model, error) {
	m, ok := c.models()[key]
	if !ok {
		return config.Model{}, fmt.Errorf("unknown model %q (add it under 'models:' in %s)", key, ConfigPath())
	}
	return m, nil
}

// models is the effective model map: supatree's own, else (for now) the one
// workbench's config defines, else the built-in defaults.
func (c *Config) models() map[string]config.Model {
	if len(c.Models) > 0 {
		return c.Models
	}
	if wb := c.legacyModels(); wb != nil {
		return wb.Models
	}
	return config.DefaultModels()
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

// ResolveNotifyCommand returns the configured notifier argv or the default.
func (c *Config) ResolveNotifyCommand() []string {
	if len(c.NotifyCommand) > 0 {
		return c.NotifyCommand
	}
	return DefaultNotifyCommand()
}

// DefaultWatchInterval is how often `supatree watch` re-derives status. It
// matches the sidebar's tick; the gh fetch behind it is separately gated on
// PRStaleAge, so this controls responsiveness rather than quota.
const DefaultWatchInterval = 30 * time.Second

// ResolveWatchInterval returns the configured watch interval or the default. An
// unparseable or absurdly short value falls back rather than failing: this is a
// daemon, and a typo in a config file must not stop it polling.
func (c *Config) ResolveWatchInterval() time.Duration {
	d, err := time.ParseDuration(c.WatchInterval)
	if err != nil || d < 5*time.Second {
		return DefaultWatchInterval
	}
	return d
}

package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/panamafrancis/workbench/pkg/config"
)

// trustFlag is the field Claude Code sets under projects["<dir>"] once the user
// has answered "Do you trust the files in this folder?" for that directory.
const trustFlag = "hasTrustDialogAccepted"

// TrustDir records dir as a trusted project directory in Claude's config, so an
// agent launched there is not asked "Do you trust the files in this folder?" on
// every start.
//
// Claude asks once per directory and records the answer under
// projects["<dir>"].hasTrustDialogAccepted in ~/.claude.json. Accepting is
// enough for a directory a single agent owns. A supatree root is not: several
// named agents run in it at once, and each rewrites that whole file from the
// snapshot it read at startup — so an agent that started before the dialog was
// accepted writes the unaccepted entry back over it, and the prompt returns on
// the next launch, forever. Seeding the flag before any agent starts breaks the
// loop: every process then reads an already-trusted entry and writes it back
// unchanged.
//
// It is best effort by design. No Claude config, an unreadable one, or one this
// process cannot write means the user answers a prompt — never a failed open —
// so callers log the error at most.
func TrustDir(dir string) error {
	if dir == "" {
		return nil
	}
	path, err := claudeConfigPath()
	if err != nil {
		return err
	}
	// Serialize against other workbench processes doing the same thing. The lock
	// lives in workbench's own cache dir rather than beside Claude's config, so
	// seeding never leaves a stray file in ~/.claude. Claude itself takes no
	// such lock, which is why we write as rarely as possible (see the
	// already-trusted short circuit below).
	return config.WithFileLock(filepath.Join(config.CacheDir(), "claude-trust.lock"), func() error {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read claude config: %w", err)
		}
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse claude config: %w", err)
		}

		projects, _ := cfg["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			cfg["projects"] = projects
		}
		entry, _ := projects[dir].(map[string]any)
		if entry == nil {
			entry = newProjectEntry()
			projects[dir] = entry
		}
		if trusted, _ := entry[trustFlag].(bool); trusted {
			// Already trusted: leave the file alone. Every write is a chance to
			// lose a concurrent Claude process's changes, so we take none we
			// don't need.
			return nil
		}
		entry[trustFlag] = true

		out, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal claude config: %w", err)
		}
		return writeFileAtomic(path, out)
	})
}

// newProjectEntry is the shape Claude itself writes for a directory it has seen
// but not been trusted with. Seeding the same fields keeps a seeded entry
// indistinguishable from one Claude created.
func newProjectEntry() map[string]any {
	return map[string]any{
		"allowedTools":                            []any{},
		"history":                                 []any{},
		"mcpContextUris":                          []any{},
		"mcpServers":                              map[string]any{},
		"enabledMcpjsonServers":                   []any{},
		"disabledMcpjsonServers":                  []any{},
		"hasClaudeMdExternalIncludesApproved":     false,
		"hasClaudeMdExternalIncludesWarningShown": false,
	}
}

// claudeConfigPath returns the real path of Claude's config. It is commonly a
// symlink into ~/.claude/, and an atomic write renames onto the path it is
// given — onto the link itself, that would replace the user's symlink with a
// regular file, so the link is resolved here instead.
func claudeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(home, ".claude.json"))
	if err != nil {
		return "", fmt.Errorf("locate claude config: %w", err)
	}
	return path, nil
}

// writeFileAtomic replaces path via a temp file in the same directory, so a
// crash mid-write cannot truncate a config this process does not own.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".workbench.tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write claude config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace claude config: %w", err)
	}
	return nil
}

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/panamafrancis/workbench/pkg/config"
)

func BuildNonoArgs(worktreePath, modelKey string, cfg *config.Config) ([]string, error) {
	m, ok := cfg.Models[modelKey]
	if !ok {
		return nil, fmt.Errorf("unknown model %q (add it under 'models:' in config)", modelKey)
	}
	args := []string{"run", "--profile", m.NonoProfile, "--allow", worktreePath, "--"}
	args = append(args, m.Binary)
	args = append(args, m.Args...)
	// Only resume (e.g. claude --continue) when a prior session exists for this
	// worktree; otherwise the binary would error on a fresh worktree.
	if len(m.ResumeArgs) > 0 && hasPriorSession(worktreePath) {
		args = append(args, m.ResumeArgs...)
	}
	return args, nil
}

// hasPriorSession reports whether a claude session transcript already exists for
// worktreePath. Claude stores transcripts at
// ~/.claude/projects/<encoded-path>/<session>.jsonl, where the path is encoded
// by replacing every non-alphanumeric character with a dash.
func hasPriorSession(worktreePath string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	dir := filepath.Join(home, ".claude", "projects", encodeProjectPath(worktreePath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			return true
		}
	}
	return false
}

// ClearSessionCache removes any cached agent session transcripts for
// worktreePath (claude stores them under ~/.claude/projects/<encoded-path>/).
// Call this when a worktree is deleted so a future worktree created at the same
// path is not silently resumed into an unrelated session via --continue. It is
// a no-op when no cache directory exists.
func ClearSessionCache(worktreePath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".claude", "projects", encodeProjectPath(worktreePath))
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove session cache: %w", err)
	}
	return nil
}

func encodeProjectPath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

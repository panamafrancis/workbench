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
	if len(m.ResumeArgs) > 0 && HasPriorSession(worktreePath) {
		args = append(args, m.ResumeArgs...)
	}
	return args, nil
}

// BuildAgentNonoArgs builds nono args for launching a named agent in
// worktreePath. When the model defines session-ID args and sessionID != "", the
// launch is pinned to that session — NewSessionArgs for a fresh start,
// ResumeSessionArgs to resume — with every "{session_id}" token replaced by
// sessionID. This lets several agents share one directory (a supatree root) yet
// resume independently. When the model has no session-ID args, it falls back to
// BuildNonoArgs semantics (append ResumeArgs iff a prior directory session
// exists), which only supports a single directory-scoped agent.
func BuildAgentNonoArgs(worktreePath, modelKey string, cfg *config.Config, sessionID string, resume bool) ([]string, error) {
	m, ok := cfg.Models[modelKey]
	if !ok {
		return nil, fmt.Errorf("unknown model %q (add it under 'models:' in config)", modelKey)
	}
	args := []string{"run", "--profile", m.NonoProfile, "--allow", worktreePath, "--"}
	args = append(args, m.Binary)
	args = append(args, m.Args...)
	switch {
	case sessionID != "" && resume && len(m.ResumeSessionArgs) > 0:
		args = append(args, substituteSession(m.ResumeSessionArgs, sessionID)...)
	case sessionID != "" && !resume && len(m.NewSessionArgs) > 0:
		args = append(args, substituteSession(m.NewSessionArgs, sessionID)...)
	case len(m.ResumeArgs) > 0 && HasPriorSession(worktreePath):
		args = append(args, m.ResumeArgs...)
	}
	return args, nil
}

// SupportsSessions reports whether modelKey defines explicit session-ID launch
// args, i.e. whether multiple independently-resumable agents can share one
// directory. Callers use this to fall back to a single directory-scoped agent.
func SupportsSessions(modelKey string, cfg *config.Config) bool {
	m, ok := cfg.Models[modelKey]
	return ok && len(m.NewSessionArgs) > 0
}

func substituteSession(args []string, id string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "{session_id}", id)
	}
	return out
}

// HasPriorSession reports whether a claude session transcript already exists for
// worktreePath. Claude stores transcripts at
// ~/.claude/projects/<encoded-path>/<session>.jsonl, where the path is encoded
// by replacing every non-alphanumeric character with a dash. It doubles as the
// "is the Claude history still around?" check that gates reusing a retired
// worktree/city name.
func HasPriorSession(worktreePath string) bool {
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
	if worktreePath == "" {
		// Guard against nuking ~/.claude/projects wholesale: an empty path
		// encodes to "" and would resolve to the projects root itself.
		return nil
	}
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

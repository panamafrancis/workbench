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
	return BuildNamedAgentNonoArgs(worktreePath, modelKey, cfg, sessionID, "", resume)
}

// BuildNamedAgentNonoArgs is BuildAgentNonoArgs plus a bus address: when
// agentName is non-empty and the model defines AgentNameArgs, those are
// appended with "{agent_name}" substituted, so sibling agents can address this
// one by a name we chose rather than one derived from its directory.
//
// The directory-derived default is exactly what makes naming necessary here:
// every agent in a supatree shares the tree root, so without this they would
// all derive the same name and collide.
func BuildNamedAgentNonoArgs(worktreePath, modelKey string, cfg *config.Config, sessionID, agentName string, resume bool) ([]string, error) {
	m, ok := cfg.Models[modelKey]
	if !ok {
		return nil, fmt.Errorf("unknown model %q (add it under 'models:' in config)", modelKey)
	}
	tokens := map[string]string{"{session_id}": sessionID, "{agent_name}": agentName}
	args := []string{"run", "--profile", m.NonoProfile, "--allow", worktreePath, "--"}
	args = append(args, m.Binary)
	args = append(args, m.Args...)
	switch {
	case sessionID != "" && resume && len(m.ResumeSessionArgs) > 0:
		args = append(args, substituteTokens(m.ResumeSessionArgs, tokens)...)
	case sessionID != "" && !resume && len(m.NewSessionArgs) > 0:
		args = append(args, substituteTokens(m.NewSessionArgs, tokens)...)
	case resume && len(m.ResumeArgs) > 0 && HasPriorSession(worktreePath):
		// Fallback for models without session-ID args: directory-scoped resume
		// (e.g. --continue). Only when actually resuming — a *new* agent must
		// never inherit whatever ran last in a shared directory.
		args = append(args, m.ResumeArgs...)
	}
	if agentName != "" && len(m.AgentNameArgs) > 0 {
		args = append(args, substituteTokens(m.AgentNameArgs, tokens)...)
	}
	return args, nil
}

// AppendPrompt appends the model's PromptArgs with "{prompt}" substituted, so
// the agent launches with prompt as its first message. A model without
// PromptArgs, or an empty prompt, leaves args unchanged: the agent starts idle,
// which is what every launch did before this existed.
func AppendPrompt(args []string, modelKey string, cfg *config.Config, prompt string) []string {
	m, ok := cfg.Models[modelKey]
	if !ok || prompt == "" || len(m.PromptArgs) == 0 {
		return args
	}
	return append(args, substituteTokens(m.PromptArgs, map[string]string{"{prompt}": prompt})...)
}

// SessionExists reports whether a transcript for sessionID already exists under
// worktreePath's claude project directory. It is the authoritative "should I
// resume?" signal: a session ID that was generated but never launched (or was
// launched under a model that ignored it) has no transcript, so the agent
// starts fresh with --session-id rather than failing to --resume a phantom id.
func SessionExists(worktreePath, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	p := filepath.Join(home, ".claude", "projects", encodeProjectPath(worktreePath), sessionID+".jsonl")
	_, err = os.Stat(p)
	return err == nil
}

// SupportsSessions reports whether modelKey defines explicit session-ID launch
// args, i.e. whether multiple independently-resumable agents can share one
// directory. Callers use this to fall back to a single directory-scoped agent.
func SupportsSessions(modelKey string, cfg *config.Config) bool {
	m, ok := cfg.Models[modelKey]
	return ok && len(m.NewSessionArgs) > 0
}

// substituteTokens replaces every token in each argument. It is a whole-argv
// pass rather than a per-flag one because a model entry may put the token
// anywhere — "--name={agent_name}" is as valid as a separate argument.
func substituteTokens(args []string, tokens map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		for tok, val := range tokens {
			a = strings.ReplaceAll(a, tok, val)
		}
		out[i] = a
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

// Grants is a sandbox's filesystem reach. nono's -a/-r are both repeatable, so
// a process can be given several writable roots and several read-only ones.
//
// It exists for the PM agent, whose threat model is close to the inverse of a
// coding agent's: it executes nothing but reads text other people wrote, and it
// needs to see every supatree while being able to write almost none of them.
// The invariant to preserve is not "read-only on trees" — it may write
// supatree's own state — but that it may never write a member repo's working
// tree.
type Grants struct {
	Allow []string // read+write
	Read  []string // read-only
}

// BuildGrantedNonoArgs builds nono args for a process with an explicit set of
// filesystem grants rather than a single worktree.
//
// agentName, when the model defines AgentNameArgs, gives it a bus address the
// same way a supatree agent gets one.
func BuildGrantedNonoArgs(modelKey string, cfg *config.Config, g Grants, sessionDir, agentName string, resume bool) ([]string, error) {
	m, ok := cfg.Models[modelKey]
	if !ok {
		return nil, fmt.Errorf("unknown model %q (add it under 'models:' in config)", modelKey)
	}
	if len(g.Allow) == 0 && len(g.Read) == 0 {
		return nil, fmt.Errorf("refusing to build a sandbox with no filesystem grants")
	}
	args := []string{"run", "--profile", m.NonoProfile}
	for _, p := range g.Allow {
		args = append(args, "--allow", p)
	}
	for _, p := range g.Read {
		args = append(args, "--read", p)
	}
	args = append(args, "--", m.Binary)
	args = append(args, m.Args...)
	if resume && len(m.ResumeArgs) > 0 && HasPriorSession(sessionDir) {
		args = append(args, m.ResumeArgs...)
	}
	if agentName != "" && len(m.AgentNameArgs) > 0 {
		args = append(args, substituteTokens(m.AgentNameArgs, map[string]string{"{agent_name}": agentName})...)
	}
	return args, nil
}

// ArchiveSessionCache moves a worktree's agent transcripts into destDir instead
// of leaving them to be deleted, and reports how many it moved.
//
// It exists because ClearSessionCache is otherwise the largest source of
// forgetting in the system: removing a worktree deletes every transcript of
// every agent that ever worked in it, and everything they learned goes with it.
// Archiving is deterministic and cheap, which is what makes it safe to put on
// the removal path — distilling a transcript into something worth keeping is a
// judgement call, and blocking a removal on an agent's round trip would be
// worse than the leak it fixes. So: move now, distil later.
//
// Best effort by contract: a failed archive must never block a removal.
func ArchiveSessionCache(worktreePath, destDir string) (int, error) {
	if worktreePath == "" || destDir == "" {
		return 0, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}
	src := filepath.Join(home, ".claude", "projects", encodeProjectPath(worktreePath))
	entries, readErr := os.ReadDir(src)
	if readErr != nil {
		// No transcript directory is the ordinary case for a worktree nobody
		// opened an agent in. Reporting it as an error would make every removal
		// of an unused worktree look like a failure, so it is deliberately not
		// one.
		return 0, nil //nolint:nilerr // absence of transcripts is not a failure
	}
	moved := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if moved == 0 {
			if err := os.MkdirAll(destDir, 0755); err != nil {
				return 0, fmt.Errorf("create archive dir: %w", err)
			}
		}
		from := filepath.Join(src, e.Name())
		to := filepath.Join(destDir, e.Name())
		if err := os.Rename(from, to); err != nil {
			// Rename fails across filesystems; fall back to a copy so the
			// transcript survives even when $HOME and the archive differ.
			if err := copyFile(from, to); err != nil {
				return moved, fmt.Errorf("archive %s: %w", e.Name(), err)
			}
			_ = os.Remove(from)
		}
		moved++
	}
	return moved, nil
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0644)
}

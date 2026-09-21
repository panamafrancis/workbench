package supatree

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
)

var infoTmpl = template.Must(template.New("info").Funcs(template.FuncMap{"join": strings.Join}).Parse(`# Supatree: {{.Name}}

_Generated file — do not edit. Regenerated on create, sync, and rename-branch._

- **Slug** (branch prefix): ` + "`st/{{.Slug}}`" + `
- **Stack**: {{.Stack}}
- **Root**: {{.Root}}
- **Model**: {{.Model}}
{{if .Intent}}- **Intent**: {{.Intent}}
{{end}}

## Member repositories

You are at the root of a multi-repo supatree. Each directory below is an
independent git worktree with its own branch — commit in each repo separately.

| Repo | Directory | Branch | Depends on |
|------|-----------|--------|------------|
{{range .Members}}| {{.Alias}} | ./{{$.ReposDir}}/{{.Alias}}/ | {{.Branch}} | {{if .DependsOn}}{{join .DependsOn ", "}}{{else}}—{{end}} |
{{end}}
## Merge order

Create and merge PRs in dependency order:

{{.MergeOrder}}

## Working here

- Each ` + "`./{{.ReposDir}}/<repo>/`" + ` is a separate git worktree — ` + "`cd`" + ` into it to run that repo's tooling and to commit.
- Branches share the slug ` + "`st/{{.Slug}}/…`" + `. Before opening any PR, give the slug a meaningful name with the ` + "`rename_branches`" + ` MCP tool (or ` + "`supatree rename-branch`" + `).
- Open PRs with the supatree MCP tools (` + "`create_pr`" + ` / ` + "`create_prs`" + `), never with bare ` + "`gh pr create`" + ` — the tools enforce dependency order and cross-link the PRs.
- To add or remove a repo, edit ` + "`supatree.yml`" + ` and run the ` + "`sync`" + ` tool (or ` + "`supatree sync`" + `).
- MCP tools available: ` + "`supatree_info`, `sync`, `rename_branches`, `create_pr`, `create_prs`, `pr_status`, `pr_comments`, `agents`, `message_agent`, `inbox`, `docs`" + `.

{{if .Memory}}## Memory

Durable notes for this stack live in ` + "`" + `{{.NotesDir}}/` + "`" + ` in the stack repo. Most
recently touched:

{{.Memory}}
Search the rest with the ` + "`recall`" + ` tool rather than reading them all — this list
is capped so that starting a session stays cheap.

{{end}}## Other agents

Several agents can share this supatree. ` + "`agents`" + ` lists them with their bus
addresses; ` + "`message_agent`" + ` leaves one a message.

**Check ` + "`inbox`" + ` at the start of each turn.** Messages are delivered to a
mailbox rather than interrupting you, so nothing tells you one has arrived —
an unread message is one nobody has acted on.
`))

// infoMemoryItems caps how many notes info.md names at session start. Storage
// was never the hard problem; what loads into every context by default is.
const infoMemoryItems = 5

// InfoData is the template context for info.md.
type infoData struct {
	*Instance
	ReposDir   string
	MergeOrder string
	Intent     string
	Memory     string
	NotesDir   string
}

// WriteInfo (re)generates .supatree/info.md for a supatree.
func WriteInfo(inst *Instance) error {
	if err := os.MkdirAll(StateDir(inst.Root), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data := infoData{
		Instance:   inst,
		ReposDir:   ReposDirName,
		MergeOrder: mergeOrderLine(inst),
		NotesDir:   NotesDirName,
	}
	if meta, err := LoadMeta(inst.Root); err == nil {
		data.Intent = meta.Intent
	}
	// The injection budget: a capped list of the most recently touched notes,
	// with everything else behind `recall`. Write-time is cheap; read-time is
	// what has to be rationed.
	if cfg, err := Load(); err == nil {
		if stack := cfg.FindStack(inst.Stack); stack != nil {
			data.Memory = MemorySummary(stack.Path, infoMemoryItems)
		}
	}
	var buf bytes.Buffer
	if err := infoTmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("render info: %w", err)
	}
	path := InfoPath(inst.Root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("write info: %w", err)
	}
	return os.Rename(tmp, path)
}

func mergeOrderLine(inst *Instance) string {
	if len(inst.Members) == 0 {
		return "_(no members)_"
	}
	names := make([]string, len(inst.Members))
	for i, m := range inst.Members {
		names[i] = m.Alias
	}
	return strings.Join(names, " → ")
}

// scaffoldAgentsMD is the AGENTS.md written into a freshly scaffolded stack
// repo. It is tracked and hand-editable; supatree never regenerates it.
const scaffoldAgentsMD = `# Supatree agent guide

This is a supatree: a set of git worktrees, one per repo, for a single
cross-repo issue. When opened, you run at the supatree root and each member repo
is checked out under ` + "`repos/<alias>/`" + `.

Read ` + "`.supatree/info.md`" + ` for the current member list, branches, and merge order.

## Conventions

- Commit in each repo's worktree separately (` + "`cd repos/<alias>`" + `).
- Give the shared branch slug a meaningful name before any PR:
  ` + "`rename_branches`" + ` MCP tool, or ` + "`supatree rename-branch <slug>`" + `.
- Open PRs with the ` + "`create_pr`" + ` / ` + "`create_prs`" + ` MCP tools (dependency-ordered,
  cross-linked) — not with bare ` + "`gh pr create`" + `.
- To change the repo set, edit ` + "`supatree.yml`" + ` then run the ` + "`sync`" + ` tool.

Add your issue-specific instructions below.
`

// scaffoldGitignore keeps the gitignored per-tree working files out of the
// stack repo's history.
const scaffoldGitignore = ReposDirName + "/\n" + stateDirName + "/\nagents/\n"

// boardHeader is prepended to every board write, so a board that has gone stale
// says so itself rather than looking like current fact.
const boardHeader = `<!-- Generated by the supatree PM. Do not edit: it is rewritten wholesale. -->
`

// WriteBoard replaces a supatree's status board.
//
// The board exists because status must not mean "read the PM's chat log".
// Scrollback is a terrible status display — it is append-only, unordered by
// importance, and unreadable a day later — so the chat is where you negotiate
// and the board is where you check.
func WriteBoard(inst *Instance, markdown string) error {
	if err := os.MkdirAll(StateDir(inst.Root), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	body := boardHeader + "\n# " + inst.Name + "\n\n" + strings.TrimSpace(markdown) + "\n"
	path := BoardPath(inst.Root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0644); err != nil {
		return fmt.Errorf("write board: %w", err)
	}
	return os.Rename(tmp, path)
}

// ReadBoard returns a supatree's board, or "" when it has none.
func ReadBoard(root string) string {
	data, err := os.ReadFile(BoardPath(root))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(string(data), boardHeader)
}

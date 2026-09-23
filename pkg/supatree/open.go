package supatree

import (
	"fmt"
	"io"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/sandbox"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

// TabName is the Zellij tab identity for an agent: "<name>" for the primary
// "main" agent, "<name>:<agent>" otherwise.
func TabName(tree, agent string) string {
	if agent == "main" {
		return tree
	}
	return tree + ":" + agent
}

// OpenRootAgent opens or resumes a named agent running at the supatree root
// (nono --allow the whole tree). Several agents share the root but resume
// independently via their session IDs. On first open it runs startup scripts
// (member repos + the stack's scripts/startup), reporting failures to startupW.
func OpenRootAgent(inst *Instance, wb *config.Config, ws zellij.Workspace, sidebarWidth, agentName, modelOverride string, startupW io.Writer) (bool, error) {
	if agentName == "" {
		agentName = "main"
	}
	model := inst.Model
	if modelOverride != "" {
		model = modelOverride
	}
	agent, _, err := EnsureAgent(inst.Root, inst.Name, agentName, model, time.Now())
	if err != nil {
		return false, err
	}
	// Several agents share this directory, which is what makes Claude's folder
	// trust never stick here (see sandbox.TrustDir). Seed it before launching;
	// failing to is a prompt the user answers, not a reason to refuse to open.
	if err := sandbox.TrustDir(inst.Root); err != nil && startupW != nil {
		_, _ = fmt.Fprintf(startupW, "warning: could not pre-trust %s with claude: %v\n", inst.Root, err)
	}
	// Resume only when this agent's session transcript actually exists; a freshly
	// created agent (or one whose id was never launched) starts a new session so
	// it never lands in another agent's chat.
	resume := sandbox.SessionExists(inst.Root, agent.SessionID)
	nonoArgs, err := sandbox.BuildNamedAgentNonoArgs(inst.Root, agent.Model, wb, agent.SessionID, agent.Address, resume)
	if err != nil {
		return false, err
	}
	// Mail waiting means somebody briefed this agent before it was running —
	// the PM starting it, or a sibling. Without a first message it would sit
	// at an empty prompt until a human typed, and the brief would go unread.
	// Ignored when the tab is already live: OpenOrFocusTab only focuses it.
	if HasMail(inst.Root, agentName) {
		nonoArgs = sandbox.AppendPrompt(nonoArgs, agent.Model, wb, KickoffPrompt)
	}
	env := inst.AgentEnv(agentName)
	tabCreated, err := ws.OpenOrFocusTab(TabName(inst.Name, agentName), inst.Root, sidebarWidth, nonoArgs, env)
	if err != nil {
		return false, err
	}
	if tabCreated {
		RunStartupScripts(inst, wb, startupW)
	}
	return tabCreated, nil
}

// requireMember resolves a member alias to a worktree that actually exists on
// disk — the shared precondition of every "open this member" entry point.
func requireMember(inst *Instance, alias string) (*Member, error) {
	m := inst.FindMember(alias)
	if m == nil {
		return nil, fmt.Errorf("repo %q is not a member of supatree %q", alias, inst.Name)
	}
	if !m.Exists {
		return nil, fmt.Errorf("member %q not created yet — run: supatree sync %s", alias, inst.Name)
	}
	return m, nil
}

// OpenMemberAgent opens an agent scoped to a single member repo (nono --allow
// just that repo). Member worktrees have unique paths, so directory-based
// resume works without session IDs.
func OpenMemberAgent(inst *Instance, wb *config.Config, ws zellij.Workspace, sidebarWidth, alias, modelOverride string) (bool, error) {
	m, err := requireMember(inst, alias)
	if err != nil {
		return false, err
	}
	model := inst.Model
	if modelOverride != "" {
		model = modelOverride
	}
	nonoArgs, err := sandbox.BuildNonoArgs(m.Path, model, wb)
	if err != nil {
		return false, err
	}
	env := inst.AgentEnv(alias)
	env["SUPATREE_MEMBER"] = alias
	return ws.OpenOrFocusTab(TabName(inst.Name, alias), m.Path, sidebarWidth, nonoArgs, env)
}

// OpenMemberShell opens a plain shell pane in the caller's current tab, rooted
// at a member repo's worktree. It is the "take me there" counterpart to
// OpenMemberAgent: no sandbox, no session, no tab — the member rows in the
// sidebar report state (branch, dirty, PR), so the obvious thing to do with one
// is stand in it.
func OpenMemberShell(inst *Instance, alias string) error {
	m, err := requireMember(inst, alias)
	if err != nil {
		return err
	}
	if !zellij.IsInZellij() {
		return fmt.Errorf("not inside zellij — cd %s", m.Path)
	}
	// Named for the location, matching the agent panes' "{repo}/{worktree}"
	// display-name convention.
	return zellij.NewPane(inst.Name+"/"+alias, m.Path)
}

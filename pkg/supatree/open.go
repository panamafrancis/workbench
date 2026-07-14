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
	agent, created, err := EnsureAgent(inst.Root, agentName, model, time.Now())
	if err != nil {
		return false, err
	}
	nonoArgs, err := sandbox.BuildAgentNonoArgs(inst.Root, agent.Model, wb, agent.SessionID, !created)
	if err != nil {
		return false, err
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

// OpenMemberAgent opens an agent scoped to a single member repo (nono --allow
// just that repo). Member worktrees have unique paths, so directory-based
// resume works without session IDs.
func OpenMemberAgent(inst *Instance, wb *config.Config, ws zellij.Workspace, sidebarWidth, alias, modelOverride string) (bool, error) {
	m := inst.FindMember(alias)
	if m == nil {
		return false, fmt.Errorf("repo %q is not a member of supatree %q", alias, inst.Name)
	}
	if !m.Exists {
		return false, fmt.Errorf("member %q not created yet — run: supatree sync %s", alias, inst.Name)
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

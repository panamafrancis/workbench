package zellij

import "github.com/panamafrancis/workbench/pkg/config"

// Workspace holds the layout/session/sidebar parameters that differ between
// tools sharing this package (workbench and supatree). Pure `zellij action`
// wrappers (GoToTab, TabNames, RenameTab, ListSessions, ...) remain free
// functions because they are workspace-agnostic; only layout writing, tab
// opening, and session creation vary and hang off a Workspace.
type Workspace struct {
	// LayoutsDir is where generated .kdl layout files are written.
	LayoutsDir string
	// SidebarCommand is the CLI invoked in the sidebar pane, e.g. "workbench ls".
	SidebarCommand string
	// SidebarEnvVar is set to "1" in the sidebar pane so the CLI knows it is
	// running as a sidebar, e.g. "WORKBENCH_SIDEBAR".
	SidebarEnvVar string
	// SidebarActiveEnvVar, when non-empty, is injected into a tab's sidebar pane
	// with the tab name as its value so the sidebar can mark the active row
	// ("you are here"), e.g. "WORKBENCH_WORKTREE_NAME". Empty to disable.
	SidebarActiveEnvVar string
	// SessionPrefix namespaces this tool's zellij sessions, e.g. "wb-".
	SessionPrefix string
	// SessionTab is the name of the initial tab in a fresh session layout.
	SessionTab string
}

// WorkbenchWorkspace returns the Workspace for the workbench tool.
func WorkbenchWorkspace() Workspace {
	return Workspace{
		LayoutsDir:          config.LayoutsDir(),
		SidebarCommand:      "workbench ls",
		SidebarEnvVar:       "WORKBENCH_SIDEBAR",
		SidebarActiveEnvVar: "WORKBENCH_WORKTREE_NAME",
		SessionPrefix:       "wb-",
		SessionTab:          "workbench",
	}
}

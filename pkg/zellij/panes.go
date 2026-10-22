package zellij

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// sidebarPaneName is the name WriteTabLayout gives a tab's sidebar pane. Every
// other terminal pane in an agent tab is the agent, or a shell the user opened
// beside it.
const sidebarPaneName = "sidebar"

// Pane is one pane as `list-panes --json` reports it, reduced to what callers
// here need.
type Pane struct {
	ID       int    `json:"id"`
	IsPlugin bool   `json:"is_plugin"`
	Title    string `json:"title"`
	Exited   bool   `json:"exited"`
	IsHeld   bool   `json:"is_held"`
	TabID    int    `json:"tab_id"`
	TabName  string `json:"tab_name"`
	Command  string `json:"pane_command"`
}

// ListPanes lists every pane in session ("" for the caller's own).
func ListPanes(session string) ([]Pane, error) {
	stdout, _, err := runZellijIn(session, "list-panes", "--all", "--json")
	if err != nil {
		return nil, fmt.Errorf("zellij list-panes: %w", err)
	}
	return parsePanes(stdout)
}

func parsePanes(out string) ([]Pane, error) {
	var panes []Pane
	if err := json.Unmarshal([]byte(out), &panes); err != nil {
		return nil, fmt.Errorf("parse list-panes: %w", err)
	}
	return panes, nil
}

// Liveness is whether a tab's command is still running. Unknown is a real
// answer, not an error to round off: a tab is replaced only when it is Dead,
// because replacing a live agent's tab starts a second agent on the same
// session and leaves the first running with no pane.
type Liveness int

const (
	LivenessUnknown Liveness = iota
	LivenessAlive
	LivenessDead
)

func (l Liveness) String() string {
	switch l {
	case LivenessAlive:
		return "alive"
	case LivenessDead:
		return "dead"
	case LivenessUnknown:
		return "unknown"
	}
	return "unknown"
}

// TabLiveness reports whether the tab named name in session still runs
// anything besides its sidebar. It reads list-panes, which says outright
// whether each pane has exited, and falls back to the dumped layout on a
// zellij too old for it.
func TabLiveness(session, name string) Liveness {
	if panes, err := ListPanes(session); err == nil {
		return LivenessIn(panes, name)
	}
	stdout, _, err := runZellijIn(session, "dump-layout")
	if err != nil {
		return LivenessUnknown
	}
	if layoutTabHasCommandPane(stdout, name) {
		return LivenessAlive
	}
	return LivenessDead
}

// LivenessIn is TabLiveness over a pane listing already read, for callers
// judging many tabs from one listing. A tab whose only panes are
// its sidebar is dead (the agent pane closes on exit); a tab missing from the
// listing altogether is unknown — it was there a moment ago, when the caller
// looked up its name.
func LivenessIn(panes []Pane, name string) Liveness {
	seen := false
	for _, p := range panes {
		if p.TabName != name {
			continue
		}
		seen = true
		if p.IsPlugin || p.Title == sidebarPaneName {
			continue
		}
		if !p.Exited && !p.IsHeld {
			return LivenessAlive
		}
	}
	if seen {
		return LivenessDead
	}
	return LivenessUnknown
}

// layoutTabHasCommandPane is the dump-layout reading of liveness: does the tab
// hold a pane with a command other than the sidebar?
func layoutTabHasCommandPane(layout, tabName string) bool {
	inTab := false
	for _, line := range strings.Split(layout, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "tab name=\""+tabName+"\"") {
			inTab = true
			continue
		}
		if inTab && strings.HasPrefix(trimmed, "tab ") {
			break
		}
		if inTab && strings.Contains(trimmed, "command=") && !strings.Contains(trimmed, "name=\""+sidebarPaneName+"\"") {
			return true
		}
	}
	return false
}

// AgentPane finds the running agent pane of the tab named name. Every agent
// tab launches its agent in nono, so the candidates are the panes running nono:
// the one WriteTabLayout titled after the tab (or "<alias>/<tab>") wins, else
// — since an agent may retitle its own terminal — the only one. A title alone
// is not enough while the pane's command is known to be something else: a
// user's shell rooted in the worktree is commonly titled with a path ending in
// the tab's name, and the caller is about to type into the match. Only when the
// listing carries no command at all does a lone title match count.
func AgentPane(session, name string) (Pane, bool, error) {
	panes, err := ListPanes(session)
	if err != nil {
		return Pane{}, false, err
	}
	p, ok := agentPane(panes, name)
	return p, ok, nil
}

func agentPane(panes []Pane, name string) (Pane, bool) {
	var nono, titledUnknown []Pane
	for _, p := range panes {
		if p.TabName != name || p.IsPlugin || p.Exited || p.IsHeld || p.Title == sidebarPaneName {
			continue
		}
		titled := p.Title == name || strings.HasSuffix(p.Title, "/"+name)
		switch {
		case runsNono(p.Command):
			if titled {
				return p, true
			}
			nono = append(nono, p)
		case titled && strings.TrimSpace(p.Command) == "":
			titledUnknown = append(titledUnknown, p)
		}
	}
	if len(nono) == 1 {
		return nono[0], true
	}
	if len(nono) == 0 && len(titledUnknown) == 1 {
		return titledUnknown[0], true
	}
	return Pane{}, false
}

// runsNono reports whether a pane's foreground command is nono, the sandbox
// WriteTabLayout starts every agent in.
func runsNono(command string) bool {
	fields := strings.Fields(command)
	return len(fields) > 0 && filepath.Base(fields[0]) == "nono"
}

// submitEnterDelay is how long SubmitToPane waits between typing the text and
// pressing enter. write-chars sends no bracketed-paste markers, so the text
// reaches the agent as a burst of keystrokes, and a zellij action returns
// before the server has written to the pty: back to back, the carriage return
// can arrive in the same read as the text. Codex's TUI takes an enter that
// comes within a few milliseconds of a keystroke burst for a newline inside a
// paste, not a submit (codex-rs tui/src/bottom_pane/paste_burst.rs), which
// leaves the text sitting unsent in its prompt. Any gap of 50ms submitted
// reliably by hand; this leaves a wide margin. A var so tests can shorten it.
var submitEnterDelay = 300 * time.Millisecond

// zellijIn is runZellijIn, swapped out by tests that need to see the actions
// sent rather than run them.
var zellijIn = runZellijIn

// SubmitToPane types text into a terminal pane and presses enter, without
// focusing it — how a running, idle agent is handed input it would otherwise
// only find on a turn it is not going to take. Callers must pass fixed text:
// whatever this writes, the agent reads as its user's words. It blocks for
// submitEnterDelay.
func SubmitToPane(session string, paneID int, text string) error {
	id := "terminal_" + strconv.Itoa(paneID)
	if _, stderr, err := zellijIn(session, "write-chars", "--pane-id", id, text); err != nil {
		return paneErr("write-chars", stderr, err)
	}
	// A carriage return, sent on its own and only after submitEnterDelay: in
	// the same write it would land in a paste and be taken as part of the text
	// rather than as enter, and too soon after it Codex does the same.
	time.Sleep(submitEnterDelay)
	if _, stderr, err := zellijIn(session, "write", "--pane-id", id, "13"); err != nil {
		return paneErr("write", stderr, err)
	}
	return nil
}

func paneErr(action, stderr string, err error) error {
	if s := strings.TrimSpace(stderr); s != "" {
		return fmt.Errorf("zellij %s: %s", action, s)
	}
	return fmt.Errorf("zellij %s: %w", action, err)
}

package zellij

import (
	"slices"
	"testing"
)

func TestParseTabIDs(t *testing.T) {
	out := `TAB_ID  POSITION  NAME
0  0  workbench
1  1  pr-cache-identity
7  2  click-tracking:comments
`
	ids := parseTabIDs(out)
	want := map[string]int{"workbench": 0, "pr-cache-identity": 1, "click-tracking:comments": 7}
	for name, id := range want {
		if ids[name] != id {
			t.Errorf("id for %q = %d, want %d", name, ids[name], id)
		}
	}
	if len(ids) != len(want) {
		t.Errorf("parsed %d tabs, want %d (the header row must be skipped)", len(ids), len(want))
	}
}

func TestParseTabIDsNameWithSpaces(t *testing.T) {
	// Zellij's own default tab names contain a space, and a tab can be renamed
	// to anything, so the name is everything after the first two fields.
	ids := parseTabIDs("TAB_ID  POSITION  NAME\n3  0  Tab #1\n")
	if ids["Tab #1"] != 3 {
		t.Errorf("parseTabIDs = %v, want Tab #1 -> 3", ids)
	}
}

func TestParseTabIDsEmpty(t *testing.T) {
	if got := parseTabIDs(""); len(got) != 0 {
		t.Errorf("parseTabIDs(\"\") = %v, want empty", got)
	}
}

func TestNewPaneArgs(t *testing.T) {
	got := newPaneArgs("tree/api", "/trees/x/repos/api", "/bin/zsh")
	want := []string{
		"new-pane", "--direction", "right",
		"--cwd", "/trees/x/repos/api",
		"--close-on-exit",
		"--name", "tree/api",
		"--", "/bin/zsh",
	}
	if !slices.Equal(got, want) {
		t.Errorf("newPaneArgs = %v, want %v", got, want)
	}
}

// The shell is passed as an explicit command because zellij honours --cwd only
// for a command pane; a bare shell pane lands in the session's cwd instead.
func TestNewPaneArgsUnnamedStillCarriesCwdAndShell(t *testing.T) {
	got := newPaneArgs("", "/trees/x/repos/api", "/bin/zsh")
	if slices.Contains(got, "--name") {
		t.Errorf("newPaneArgs with no name should omit --name: %v", got)
	}
	want := []string{
		"new-pane", "--direction", "right",
		"--cwd", "/trees/x/repos/api",
		"--close-on-exit",
		"--", "/bin/zsh",
	}
	if !slices.Equal(got, want) {
		t.Errorf("newPaneArgs = %v, want %v", got, want)
	}
}

func TestUserShellFallsBackToBash(t *testing.T) {
	t.Setenv("SHELL", "")
	if got := userShell(); got != "bash" {
		t.Errorf("userShell() with empty SHELL = %q, want bash", got)
	}
	t.Setenv("SHELL", "/bin/fish")
	if got := userShell(); got != "/bin/fish" {
		t.Errorf("userShell() = %q, want /bin/fish", got)
	}
}

package zellij

import "testing"

func TestParseFocusedTab(t *testing.T) {
	layout := `layout {
    tab name="canberra" hide_floating_panes=true {
        pane size=30 name="sidebar" command="supatree"
    }
    tab name="darwin:reviewer" focus=true hide_floating_panes=true {
        pane name="agent" command="nono"
    }
    tab name="supatree-dash" {
        pane command="supatree"
    }
}`
	if got := parseFocusedTab(layout); got != "darwin:reviewer" {
		t.Errorf("parseFocusedTab = %q, want %q", got, "darwin:reviewer")
	}
}

// No focused tab must read as "could not tell", not as a tab named "". Callers
// use this to suppress work, and suppressing on a guess is worse than not
// suppressing at all.
func TestParseFocusedTabNoFocus(t *testing.T) {
	if got := parseFocusedTab("layout {\n    tab name=\"canberra\" {\n    }\n}"); got != "" {
		t.Errorf("parseFocusedTab = %q, want empty", got)
	}
	if got := parseFocusedTab(""); got != "" {
		t.Errorf("parseFocusedTab(empty) = %q, want empty", got)
	}
}

// A pane line may carry focus=true too; only tabs have tab names.
func TestParseFocusedTabIgnoresPanes(t *testing.T) {
	layout := `layout {
    tab name="canberra" {
        pane name="agent" focus=true command="nono"
    }
}`
	if got := parseFocusedTab(layout); got != "" {
		t.Errorf("parseFocusedTab = %q, want empty (no tab is focused)", got)
	}
}

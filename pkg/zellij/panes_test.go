package zellij

import "testing"

// Captured from zellij 0.44.3 `list-panes --all --json`, trimmed to the fields
// Pane reads plus a few it ignores.
const panesJSON = `[
 {"id":0,"is_plugin":false,"is_focused":false,"title":"sidebar","exited":false,"is_held":false,"tab_id":0,"tab_name":"berlin","pane_command":"supatree ls","pane_cwd":"/t/berlin"},
 {"id":1,"is_plugin":false,"is_focused":true,"title":"berlin","exited":false,"is_held":false,"tab_id":0,"tab_name":"berlin","pane_command":"/opt/homebrew/bin/nono run --profile supatree-agent -- claude"},
 {"id":2,"is_plugin":false,"title":"sidebar","exited":false,"is_held":false,"tab_id":1,"tab_name":"paris","pane_command":"supatree ls"},
 {"id":3,"is_plugin":false,"title":"sidebar","exited":false,"is_held":false,"tab_id":2,"tab_name":"rome","pane_command":"supatree ls"},
 {"id":4,"is_plugin":false,"title":"rome","exited":true,"is_held":false,"tab_id":2,"tab_name":"rome","pane_command":"nono"},
 {"id":5,"is_plugin":false,"title":"sidebar","exited":false,"is_held":false,"tab_id":3,"tab_name":"oslo","pane_command":"supatree ls"},
 {"id":6,"is_plugin":false,"title":"shell","exited":false,"is_held":false,"tab_id":3,"tab_name":"oslo","pane_command":"/bin/zsh"},
 {"id":7,"is_plugin":false,"title":"app/kiev","exited":false,"is_held":false,"tab_id":4,"tab_name":"kiev","pane_command":"nono"},
 {"id":8,"is_plugin":false,"title":"sidebar","exited":false,"is_held":false,"tab_id":5,"tab_name":"lima","pane_command":"supatree ls"},
 {"id":9,"is_plugin":false,"title":"st-lima-main","exited":false,"is_held":false,"tab_id":5,"tab_name":"lima","pane_command":"/opt/homebrew/bin/nono run --profile p -- claude"},
 {"id":10,"is_plugin":false,"title":"✳ Fixing bugs","exited":false,"is_held":false,"tab_id":6,"tab_name":"wb","pane_command":"/opt/homebrew/bin/nono run --profile p -- claude"},
 {"id":11,"is_plugin":false,"title":"~/trees/repos/wb","exited":false,"is_held":false,"tab_id":6,"tab_name":"wb","pane_command":"/bin/zsh"},
 {"id":0,"is_plugin":true,"title":"tab-bar","exited":false,"is_held":false,"tab_id":1,"tab_name":"paris"}
]`

func mustPanes(t *testing.T) []Pane {
	t.Helper()
	panes, err := parsePanes(panesJSON)
	if err != nil {
		t.Fatal(err)
	}
	return panes
}

func TestTabLiveness(t *testing.T) {
	panes := mustPanes(t)
	for _, tc := range []struct {
		tab  string
		want Liveness
	}{
		{"berlin", LivenessAlive},   // agent running
		{"paris", LivenessDead},     // only the sidebar (and a plugin) left
		{"rome", LivenessDead},      // agent pane exited
		{"oslo", LivenessAlive},     // a shell the user opened keeps the tab
		{"madrid", LivenessUnknown}, // not in the listing at all
	} {
		if got := LivenessIn(panes, tc.tab); got != tc.want {
			t.Errorf("LivenessIn(%s) = %s, want %s", tc.tab, got, tc.want)
		}
	}
}

// The agent is the pane titled after its tab, or the only running pane besides
// the sidebar: typing into a sidebar or the user's own shell must be
// impossible.
func TestAgentPane(t *testing.T) {
	panes := mustPanes(t)
	if p, ok := agentPane(panes, "berlin"); !ok || p.ID != 1 {
		t.Errorf("berlin agent = %+v, %v; want pane 1", p, ok)
	}
	if p, ok := agentPane(panes, "kiev"); !ok || p.ID != 7 {
		t.Errorf("kiev agent (alias-prefixed title) = %+v, %v; want pane 7", p, ok)
	}
	// An agent that retitled its terminal is still found by the nono it runs
	// in; a lone user shell (oslo) never is.
	if p, ok := agentPane(panes, "lima"); !ok || p.ID != 9 {
		t.Errorf("lima agent (retitled) = %+v, %v; want pane 9", p, ok)
	}
	// A user's shell titled with a path ending in the tab name is not the
	// agent, even when the agent has retitled itself.
	if p, ok := agentPane(panes, "wb"); !ok || p.ID != 10 {
		t.Errorf("wb agent (retitled, beside a path-titled shell) = %+v, %v; want pane 10", p, ok)
	}
	for _, tab := range []string{"paris", "rome", "oslo", "madrid"} {
		if p, ok := agentPane(panes, tab); ok {
			t.Errorf("%s has no running agent, got pane %+v", tab, p)
		}
	}
}

func TestParsePanesRejectsGarbage(t *testing.T) {
	if _, err := parsePanes("TAB_ID TAB_POS TAB_NAME"); err == nil {
		t.Error("a table-format listing parsed as JSON")
	}
}

// The dump-layout fallback, for a zellij without list-panes.
func TestLayoutTabHasCommandPane(t *testing.T) {
	layout := `layout {
    tab name="berlin" {
        pane size=30 name="sidebar" command="supatree"
        pane name="berlin" command="nono"
    }
    tab name="paris" focus=true {
        pane size=30 name="sidebar" command="supatree"
        pane
    }
}`
	if !layoutTabHasCommandPane(layout, "berlin") {
		t.Error("berlin runs nono beside its sidebar")
	}
	if layoutTabHasCommandPane(layout, "paris") {
		t.Error("paris runs nothing but its sidebar")
	}
}

func TestParseTabIDs(t *testing.T) {
	ids := parseTabIDs("TAB_ID  POSITION  NAME\n0  0  supatree\n4  1  supatree-pm:reviews\n7  2  a name with spaces\n")
	want := map[string]int{"supatree": 0, "supatree-pm:reviews": 4, "a name with spaces": 7}
	for name, id := range want {
		if ids[name] != id {
			t.Errorf("ids[%q] = %d, want %d (all: %v)", name, ids[name], id, ids)
		}
	}
	if _, ok := ids["NAME"]; ok {
		t.Error("the header row was read as a tab")
	}
}

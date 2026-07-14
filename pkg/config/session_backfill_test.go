package config

import "testing"

func TestBackfillSessionArgsFillsClaude(t *testing.T) {
	// A config that predates the session-arg fields.
	models := map[string]Model{
		"claude": {Binary: "claude", Args: []string{"--dangerously-skip-permissions"}, ResumeArgs: []string{"--continue"}},
	}
	backfillSessionArgs(models)
	m := models["claude"]
	if len(m.NewSessionArgs) == 0 || len(m.ResumeSessionArgs) == 0 {
		t.Errorf("claude should gain session args, got new=%v resume=%v", m.NewSessionArgs, m.ResumeSessionArgs)
	}
}

func TestBackfillSessionArgsRespectsExisting(t *testing.T) {
	models := map[string]Model{
		"claude": {Binary: "claude", NewSessionArgs: []string{"--mine", "{session_id}"}},
	}
	backfillSessionArgs(models)
	if got := models["claude"].NewSessionArgs; len(got) != 2 || got[0] != "--mine" {
		t.Errorf("must not overwrite user session args, got %v", got)
	}
}

func TestBackfillSessionArgsSkipsRepurposedBinary(t *testing.T) {
	// A user who repurposed the "claude" key for a different binary must not get
	// claude's flags injected.
	models := map[string]Model{
		"claude": {Binary: "some-other-cli"},
	}
	backfillSessionArgs(models)
	if len(models["claude"].NewSessionArgs) != 0 {
		t.Error("must not inject claude flags into a differently-binaried model")
	}
}

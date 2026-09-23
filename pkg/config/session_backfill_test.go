package config

import "testing"

func TestBackfillSessionArgsFillsClaude(t *testing.T) {
	// A config that predates the session-arg fields.
	models := map[string]Model{
		modelClaude: {Binary: "claude", Args: []string{"--dangerously-skip-permissions"}, ResumeArgs: []string{"--continue"}},
	}
	backfillSessionArgs(models)
	m := models[modelClaude]
	if len(m.NewSessionArgs) == 0 || len(m.ResumeSessionArgs) == 0 {
		t.Errorf("claude should gain session args, got new=%v resume=%v", m.NewSessionArgs, m.ResumeSessionArgs)
	}
}

func TestBackfillSessionArgsRespectsExisting(t *testing.T) {
	models := map[string]Model{
		modelClaude: {Binary: "claude", NewSessionArgs: []string{"--mine", "{session_id}"}},
	}
	backfillSessionArgs(models)
	if got := models[modelClaude].NewSessionArgs; len(got) != 2 || got[0] != "--mine" {
		t.Errorf("must not overwrite user session args, got %v", got)
	}
}

func TestBackfillSessionArgsSkipsRepurposedBinary(t *testing.T) {
	// A user who repurposed the "claude" key for a different binary must not get
	// claude's flags injected.
	models := map[string]Model{
		modelClaude: {Binary: "some-other-cli"},
	}
	backfillSessionArgs(models)
	if len(models[modelClaude].NewSessionArgs) != 0 {
		t.Error("must not inject claude flags into a differently-binaried model")
	}
}

func TestBackfillPromptArgs(t *testing.T) {
	// A config whose session args were already backfilled still predates
	// prompt_args, so the prompt backfill must not hang off the session one.
	models := map[string]Model{
		modelClaude: {Binary: "claude", NewSessionArgs: []string{"--session-id", "{session_id}"}},
		"shell":     {Binary: "bash"},
	}
	backfillSessionArgs(models)
	if got := models[modelClaude].PromptArgs; len(got) != 1 || got[0] != "{prompt}" {
		t.Errorf("claude should gain prompt args, got %v", got)
	}
	if got := models["shell"].PromptArgs; len(got) != 0 {
		t.Errorf("a model with no shipped prompt args must not gain any, got %v", got)
	}

	mine := map[string]Model{modelClaude: {Binary: "claude", PromptArgs: []string{"-p", "{prompt}"}}}
	backfillSessionArgs(mine)
	if got := mine[modelClaude].PromptArgs; len(got) != 2 {
		t.Errorf("must not overwrite user prompt args, got %v", got)
	}
	other := map[string]Model{modelClaude: {Binary: "some-other-cli"}}
	backfillSessionArgs(other)
	if len(other[modelClaude].PromptArgs) != 0 {
		t.Error("must not inject claude's prompt into a differently-binaried model")
	}
}

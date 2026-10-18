package config

import "testing"

func TestBackfillSessionArgsFillsClaude(t *testing.T) {
	// A config that predates the session-arg fields.
	models := map[string]Model{
		modelClaude: {Binary: "claude", Args: []string{"--dangerously-skip-permissions"}, ResumeArgs: []string{"--continue"}},
	}
	BackfillModels(models)
	m := models[modelClaude]
	if len(m.NewSessionArgs) == 0 || len(m.ResumeSessionArgs) == 0 {
		t.Errorf("claude should gain session args, got new=%v resume=%v", m.NewSessionArgs, m.ResumeSessionArgs)
	}
}

func TestBackfillSessionArgsRespectsExisting(t *testing.T) {
	models := map[string]Model{
		modelClaude: {Binary: "claude", NewSessionArgs: []string{"--mine", "{session_id}"}},
	}
	BackfillModels(models)
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
	BackfillModels(models)
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
	BackfillModels(models)
	if got := models[modelClaude].PromptArgs; len(got) != 1 || got[0] != "{prompt}" {
		t.Errorf("claude should gain prompt args, got %v", got)
	}
	if got := models["shell"].PromptArgs; len(got) != 0 {
		t.Errorf("a model with no shipped prompt args must not gain any, got %v", got)
	}

	mine := map[string]Model{modelClaude: {Binary: "claude", PromptArgs: []string{"-p", "{prompt}"}}}
	BackfillModels(mine)
	if got := mine[modelClaude].PromptArgs; len(got) != 2 {
		t.Errorf("must not overwrite user prompt args, got %v", got)
	}
	other := map[string]Model{modelClaude: {Binary: "some-other-cli"}}
	BackfillModels(other)
	if len(other[modelClaude].PromptArgs) != 0 {
		t.Error("must not inject claude's prompt into a differently-binaried model")
	}
}

// A config saved before agent_name_args existed — with session and prompt args
// already backfilled — still gains it, or every agent launches under a name
// other than the bus address it reports.
func TestBackfillAgentNameArgs(t *testing.T) {
	models := map[string]Model{
		modelClaude: {Binary: modelClaude, NewSessionArgs: []string{"--session-id", "{session_id}"}, PromptArgs: []string{"{prompt}"}},
		"shell":     {Binary: "bash"},
	}
	BackfillModels(models)
	if got := models[modelClaude].AgentNameArgs; len(got) != 2 || got[0] != "--name" || got[1] != "{agent_name}" {
		t.Errorf("claude should gain agent name args, got %v", got)
	}
	if got := models["shell"].AgentNameArgs; len(got) != 0 {
		t.Errorf("a model with no shipped name args must not gain any, got %v", got)
	}

	mine := map[string]Model{modelClaude: {Binary: modelClaude, AgentNameArgs: []string{"-n", "{agent_name}"}}}
	BackfillModels(mine)
	if got := mine[modelClaude].AgentNameArgs; got[0] != "-n" {
		t.Errorf("must not overwrite user name args, got %v", got)
	}
	other := map[string]Model{modelClaude: {Binary: "some-other-cli"}}
	BackfillModels(other)
	if len(other[modelClaude].AgentNameArgs) != 0 {
		t.Error("must not inject claude's name args into a differently-binaried model")
	}
}

// A codex entry saved before codex sessions were tracked gets the resume args
// and, with them, the store they are read against.
func TestBackfillCodexSessionStore(t *testing.T) {
	models := map[string]Model{"codex": {Binary: "codex", ResumeArgs: []string{"resume", "--last"}}}
	BackfillModels(models)
	m := models["codex"]
	if m.SessionStore != SessionStoreCodex {
		t.Errorf("SessionStore = %q, want %q", m.SessionStore, SessionStoreCodex)
	}
	if len(m.ResumeSessionArgs) == 0 || len(m.NewSessionArgs) != 0 {
		t.Errorf("session args = new %v, resume %v", m.NewSessionArgs, m.ResumeSessionArgs)
	}
	if got := m.ResumeArgs; len(got) != 2 || got[1] != "--last" {
		t.Errorf("ResumeArgs overwritten: %v", got)
	}
}

package setup

import (
	"testing"

	"github.com/panamafrancis/workbench/pkg/config"
)

func TestHasHardFailures(t *testing.T) {
	noFails := []CheckResult{
		{Status: StatusOK},
		{Status: StatusWarn},
	}
	if HasHardFailures(noFails) {
		t.Error("no failures should return false")
	}

	withFail := []CheckResult{
		{Status: StatusOK},
		{Status: StatusFail},
	}
	if !HasHardFailures(withFail) {
		t.Error("should detect failure")
	}
}

func TestCheckReposEmpty(t *testing.T) {
	cfg := &config.Config{}
	result := checkRepos(cfg)
	if result.Status != StatusWarn {
		t.Errorf("empty repos should warn, got %s", result.Status)
	}
}

func TestCheckReposPopulated(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.Repo{{Alias: "test", LocalPath: "/tmp"}},
	}
	result := checkRepos(cfg)
	if result.Status != StatusOK {
		t.Errorf("populated repos should be ok, got %s", result.Status)
	}
}

func TestCheckSSHAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	result := checkSSHAgent()
	if result.Status != StatusOK {
		t.Errorf("with SSH_AUTH_SOCK set, should be ok, got %s", result.Status)
	}

	t.Setenv("SSH_AUTH_SOCK", "")
	result = checkSSHAgent()
	if result.Status != StatusWarn {
		t.Errorf("without SSH_AUTH_SOCK, should warn, got %s", result.Status)
	}
}

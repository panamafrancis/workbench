package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/panamafrancis/workbench/pkg/config"
)

type CheckStatus string

const (
	StatusOK   CheckStatus = "ok"
	StatusWarn CheckStatus = "warn"
	StatusFail CheckStatus = "fail"
	StatusSkip CheckStatus = "skip"
)

type CheckResult struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
	Hint    string      `json:"hint,omitempty"`
}

func RunChecks(cfg *config.Config) []CheckResult {
	results := make([]CheckResult, 0, 10)
	results = append(results, checkZellij())
	results = append(results, checkNono())
	results = append(results, checkGit())
	results = append(results, checkGH())
	results = append(results, checkConfig())
	results = append(results, checkNonoProfiles(cfg)...)
	results = append(results, checkSSHAgent())
	results = append(results, checkRepos(cfg))
	return results
}

func HasHardFailures(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == StatusFail {
			return true
		}
	}
	return false
}

func checkZellij() CheckResult {
	out, err := exec.CommandContext(context.Background(), "zellij", "--version").Output()
	if err != nil {
		return CheckResult{
			Name:    "zellij",
			Status:  StatusFail,
			Message: "not found",
			Hint:    "brew install zellij",
		}
	}
	version := strings.TrimSpace(string(out))
	return CheckResult{
		Name:    "zellij",
		Status:  StatusOK,
		Message: version,
	}
}

func checkNono() CheckResult {
	path, err := exec.LookPath("nono")
	if err != nil {
		return CheckResult{
			Name:    "nono",
			Status:  StatusWarn,
			Message: "not found (workbench open will not work)",
			Hint:    "install from https://nono.sh",
		}
	}
	out, _ := exec.CommandContext(context.Background(), path, "--version").Output()
	version := strings.TrimSpace(string(out))
	if version == "" {
		version = "found at " + path
	}
	return CheckResult{
		Name:    "nono",
		Status:  StatusOK,
		Message: version,
	}
}

func checkGit() CheckResult {
	out, err := exec.CommandContext(context.Background(), "git", "version").Output()
	if err != nil {
		return CheckResult{
			Name:    "git",
			Status:  StatusFail,
			Message: "not found",
		}
	}
	return CheckResult{
		Name:    "git",
		Status:  StatusOK,
		Message: strings.TrimSpace(string(out)),
	}
}

func checkGH() CheckResult {
	_, err := exec.LookPath("gh")
	if err != nil {
		return CheckResult{
			Name:    "gh",
			Status:  StatusWarn,
			Message: "not found (optional)",
			Hint:    "brew install gh && gh auth login",
		}
	}
	cmd := exec.CommandContext(context.Background(), "gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return CheckResult{
			Name:    "gh",
			Status:  StatusWarn,
			Message: "not authenticated",
			Hint:    "gh auth login",
		}
	}
	return CheckResult{
		Name:    "gh",
		Status:  StatusOK,
		Message: "authenticated",
	}
}

func checkConfig() CheckResult {
	path := config.ConfigPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return CheckResult{
			Name:    "config",
			Status:  StatusWarn,
			Message: "no config file (using defaults)",
			Hint:    "workbench init",
		}
	}
	_, err := config.Load()
	if err != nil {
		return CheckResult{
			Name:    "config",
			Status:  StatusFail,
			Message: fmt.Sprintf("parse error: %v", err),
		}
	}
	return CheckResult{
		Name:    "config",
		Status:  StatusOK,
		Message: path,
	}
}

func checkNonoProfiles(cfg *config.Config) []CheckResult {
	if cfg == nil {
		return nil
	}
	var results []CheckResult
	home, _ := os.UserHomeDir()
	profileDir := filepath.Join(home, ".config", "nono", "profiles")
	for name, model := range cfg.Models {
		profile := model.NonoProfile
		if profile == "" {
			continue
		}
		profilePath := filepath.Join(profileDir, profile+".json")
		if _, err := os.Stat(profilePath); os.IsNotExist(err) {
			cmd := exec.CommandContext(context.Background(), "nono", "profile", "show", profile)
			if cmd.Run() != nil {
				results = append(results, CheckResult{
					Name:    fmt.Sprintf("nono profile [%s]", name),
					Status:  StatusWarn,
					Message: fmt.Sprintf("profile %q not found", profile),
					Hint:    "workbench init --profile",
				})
			}
		}
	}
	if len(results) == 0 {
		results = append(results, CheckResult{
			Name:    "nono profiles",
			Status:  StatusOK,
			Message: "all referenced profiles found",
		})
	}
	return results
}

func checkSSHAgent() CheckResult {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return CheckResult{
			Name:    "ssh-agent",
			Status:  StatusWarn,
			Message: "SSH_AUTH_SOCK not set",
			Hint:    "eval $(ssh-agent -s) && ssh-add",
		}
	}
	return CheckResult{
		Name:    "ssh-agent",
		Status:  StatusOK,
		Message: "socket available",
	}
}

func checkRepos(cfg *config.Config) CheckResult {
	if cfg == nil || len(cfg.Repos) == 0 {
		return CheckResult{
			Name:    "repos",
			Status:  StatusWarn,
			Message: "no repos registered",
			Hint:    "workbench add repo <path> --alias=<alias>",
		}
	}
	return CheckResult{
		Name:    "repos",
		Status:  StatusOK,
		Message: fmt.Sprintf("%d repo(s) registered", len(cfg.Repos)),
	}
}

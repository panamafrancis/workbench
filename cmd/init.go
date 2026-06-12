package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/setup"
)

var (
	initNonInteractive bool
	initProfile        bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up workbench for first use",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("workbench init")
		fmt.Println()

		results := setup.RunChecks(cfg)
		hardFail := false
		for _, r := range results {
			if r.Status == setup.StatusFail {
				fmt.Printf("  ✗ %s: %s\n", r.Name, r.Message)
				if r.Hint != "" {
					fmt.Printf("    → %s\n", r.Hint)
				}
				hardFail = true
			}
		}
		if hardFail {
			return fmt.Errorf("fix the above issues before continuing")
		}

		if initProfile {
			return generateNonoProfile()
		}

		if err := ensureConfig(); err != nil {
			return err
		}

		if err := offerNonoProfile(); err != nil {
			return err
		}

		if err := offerGHAuth(); err != nil {
			return err
		}

		if err := offerAddRepo(); err != nil {
			return err
		}

		if err := offerMCPRegistration(); err != nil {
			return err
		}

		fmt.Println()
		fmt.Println("Setup complete. Run: workbench start")
		return nil
	},
}

func ensureConfig() error {
	path := config.ConfigPath()
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Config exists at %s\n", path)
		return nil
	}

	fmt.Println("Creating default config...")
	c := config.DefaultConfig()

	if !initNonInteractive {
		model := promptDefault("Default model", c.DefaultModel)
		c.DefaultModel = model
	}

	if err := c.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("Wrote %s\n", path)
	cfg = c
	return nil
}

func offerNonoProfile() error {
	home, _ := os.UserHomeDir()
	profilePath := filepath.Join(home, ".config", "nono", "profiles", "claude-code-local.json")
	if _, err := os.Stat(profilePath); err == nil {
		fmt.Printf("nono profile exists at %s\n", profilePath)
		return nil
	}
	if initNonInteractive {
		return generateNonoProfile()
	}
	if !promptYN("Generate a nono sandbox profile?", true) {
		return nil
	}
	return generateNonoProfile()
}

func offerGHAuth() error {
	if initNonInteractive {
		return nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		fmt.Println("gh: not installed (skipping — PR features won't work)")
		return nil //nolint:nilerr // not-found is a skip, not an error
	}
	ghCmd := exec.CommandContext(context.Background(), "gh", "auth", "status")
	if ghCmd.Run() == nil {
		fmt.Println("gh: already authenticated")
		return nil
	}
	if !promptYN("Run gh auth login?", false) {
		return nil
	}
	cmd := exec.CommandContext(context.Background(), "gh", "auth", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func offerAddRepo() error {
	if initNonInteractive {
		return nil
	}
	if len(cfg.Repos) > 0 {
		return nil
	}
	if !promptYN("Add a repo now?", true) {
		return nil
	}
	path := promptDefault("Repo path", "")
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	alias := promptDefault("Alias", filepath.Base(abs))
	cfg.Repos = append(cfg.Repos, config.Repo{
		Alias:     alias,
		LocalPath: abs,
	})
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Added repo %q\n", alias)
	return nil
}

func offerMCPRegistration() error {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Println("claude: not installed (skipping MCP server registration)")
		return nil //nolint:nilerr // not-found is a skip
	}

	checkCmd := exec.CommandContext(context.Background(), claudePath, "mcp", "list")
	if out, err := checkCmd.Output(); err == nil && strings.Contains(string(out), "workbench") {
		fmt.Println("MCP server: already registered with Claude Code")
		return nil
	}

	if !initNonInteractive {
		if !promptYN("Register workbench MCP server with Claude Code?", true) {
			return nil
		}
	}

	wbPath, err := exec.LookPath("workbench")
	if err != nil {
		wbPath = "workbench"
	}

	cmd := exec.CommandContext(context.Background(),
		claudePath, "mcp", "add", "workbench", "-s", "user", "--", wbPath, "mcp")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: MCP registration failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "  You can register manually: claude mcp add workbench -s user -- %s mcp\n", wbPath)
		return nil //nolint:nilerr // non-fatal
	}
	fmt.Println("Registered workbench MCP server with Claude Code")
	return nil
}

func generateNonoProfile() error {
	home, _ := os.UserHomeDir()
	profileDir := filepath.Join(home, ".config", "nono", "profiles")
	profilePath := filepath.Join(profileDir, "claude-code-local.json")

	if _, err := os.Stat(profilePath); err == nil {
		fmt.Printf("Profile already exists at %s\n", profilePath)
		if initNonInteractive || !promptYN("Overwrite?", false) {
			return nil
		}
	}

	sshKeys := globSSHPublicKeys(home)

	repoDirs := []string{}
	for _, r := range cfg.Repos {
		parent := filepath.Dir(r.LocalPath)
		repoDirs = append(repoDirs, parent)
	}

	goPath := ""
	if out, err := exec.CommandContext(context.Background(), "go", "env", "GOPATH").Output(); err == nil {
		goPath = strings.TrimSpace(string(out))
	}

	allowDirs := []string{filepath.Join(home, ".workbench")}
	allowDirs = append(allowDirs, repoDirs...)
	if goPath != "" {
		allowDirs = append(allowDirs,
			filepath.Join(goPath, "pkg"),
			filepath.Join(goPath, "bin"),
			filepath.Join(goPath, "src"),
		)
	}
	ghConfigDir := filepath.Join(home, ".config", "gh")
	if _, err := os.Stat(ghConfigDir); err == nil {
		allowDirs = append(allowDirs, ghConfigDir)
	}

	readFiles := make([]string, 0, 1+len(sshKeys))
	readFiles = append(readFiles, filepath.Join(home, ".ssh", "config"))
	readFiles = append(readFiles, sshKeys...)

	allowFiles := []string{filepath.Join(home, ".ssh", "known_hosts")}
	bypassFiles := append([]string{
		filepath.Join(home, ".ssh", "config"),
		filepath.Join(home, ".ssh", "known_hosts"),
	}, sshKeys...)

	profile := buildProfileJSON(allowDirs, readFiles, allowFiles, bypassFiles)

	if err := os.MkdirAll(profileDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(profilePath, []byte(profile), 0644); err != nil {
		return err
	}
	fmt.Printf("Wrote nono profile to %s\n", profilePath)
	return nil
}

func globSSHPublicKeys(home string) []string {
	pattern := filepath.Join(home, ".ssh", "*.pub")
	matches, _ := filepath.Glob(pattern)
	return matches
}

func buildProfileJSON(allowDirs, readFiles, allowFiles, bypassFiles []string) string {
	profile := nonoProfile{
		Extends: []string{"claude-code"},
		Meta: nonoMeta{
			Name:        "claude-code-local",
			Description: "claude-code with project repos, toolchain, and SSH agent",
		},
		Filesystem: nonoFilesystem{
			Allow:             allowDirs,
			ReadFile:          readFiles,
			AllowFile:         allowFiles,
			UnixSocketSubtree: []string{"/private/tmp"},
			BypassProtection:  bypassFiles,
		},
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data) + "\n"
}

type nonoProfile struct {
	Extends    []string       `json:"extends"`
	Meta       nonoMeta       `json:"meta"`
	Filesystem nonoFilesystem `json:"filesystem"`
}

type nonoMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type nonoFilesystem struct {
	Allow             []string `json:"allow"`
	ReadFile          []string `json:"read_file"`
	AllowFile         []string `json:"allow_file"`
	UnixSocketSubtree []string `json:"unix_socket_subtree"`
	BypassProtection  []string `json:"bypass_protection"`
}

func promptDefault(label, defaultVal string) string {
	if initNonInteractive {
		return defaultVal
	}
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("%s: ", label)
	}
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

func promptYN(question string, defaultYes bool) bool {
	if initNonInteractive {
		return defaultYes
	}
	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}
	fmt.Printf("%s %s ", question, hint)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return strings.HasPrefix(line, "y")
}

func init() {
	initCmd.Flags().BoolVar(&initNonInteractive, "non-interactive", false, "accept defaults without prompting")
	initCmd.Flags().BoolVar(&initProfile, "profile", false, "generate a nono profile only")
}

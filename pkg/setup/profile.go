package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Profile is a nono profile that an init command writes. It holds only what is
// the same for every launch — toolchain, gh, the ssh agent, the tool's own
// dirs. Anything specific to one worktree is a per-launch flag instead, since
// a static file cannot know where a given worktree lives.
//
// Each tool writes its own profile under its own name, so no profile file ever
// has two writers.
type Profile struct {
	Name        string
	Description string
	Extends     []string
	Allow       []string // read+write directories
	Read        []string // read-only directories
	ReadFile    []string // read-only single files
	AllowFile   []string // read+write single files
	Bypass      []string // files nono protects by default that this profile needs
	Deny        []string // carve-outs inside an allowed directory (macOS only)
}

// NonoProfilesDir is where nono looks up user profiles by name. It is nono's
// directory, not ours, so it does not follow our XDG resolution.
func NonoProfilesDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "nono", "profiles")
}

// NonoProfilePath is the file nono resolves `--profile <name>` to.
func NonoProfilePath(name string) string {
	return filepath.Join(NonoProfilesDir(), name+".json")
}

// JSON renders the profile in nono's profile format.
func (p Profile) JSON() string {
	doc := nonoProfile{
		Extends: p.Extends,
		Meta:    nonoMeta{Name: p.Name, Description: p.Description},
		Filesystem: nonoFilesystem{
			Allow:             p.Allow,
			Read:              p.Read,
			ReadFile:          p.ReadFile,
			AllowFile:         p.AllowFile,
			UnixSocketSubtree: []string{"/private/tmp"},
			BypassProtection:  p.Bypass,
			Deny:              p.Deny,
		},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data) + "\n"
}

// Write writes the profile to nono's profile dir and returns its path.
func (p Profile) Write() (string, error) {
	if p.Name == "" {
		return "", fmt.Errorf("nono profile has no name")
	}
	if err := os.MkdirAll(NonoProfilesDir(), 0755); err != nil {
		return "", err
	}
	path := NonoProfilePath(p.Name)
	if err := os.WriteFile(path, []byte(p.JSON()), 0644); err != nil {
		return "", err
	}
	return path, nil
}

// WithToolchain adds what an agent needs to build, push and talk to GitHub
// regardless of which tool launched it: the Go module cache and bin, gh's
// config, and the ssh config, keys and known_hosts.
func (p Profile) WithToolchain() Profile {
	home, _ := os.UserHomeDir()
	if goPath := goEnvPath(); goPath != "" {
		p.Allow = append(p.Allow,
			filepath.Join(goPath, "pkg"),
			filepath.Join(goPath, "bin"),
			filepath.Join(goPath, "src"),
		)
	}
	ghConfigDir := filepath.Join(home, ".config", "gh")
	if _, err := os.Stat(ghConfigDir); err == nil {
		p.Allow = append(p.Allow, ghConfigDir)
	}

	sshKeys, _ := filepath.Glob(filepath.Join(home, ".ssh", "*.pub"))
	sshConfig := filepath.Join(home, ".ssh", "config")
	knownHosts := filepath.Join(home, ".ssh", "known_hosts")
	p.ReadFile = append(p.ReadFile, sshConfig)
	p.ReadFile = append(p.ReadFile, sshKeys...)
	p.AllowFile = append(p.AllowFile, knownHosts)
	p.Bypass = append(p.Bypass, sshConfig, knownHosts)
	p.Bypass = append(p.Bypass, sshKeys...)
	return p
}

func goEnvPath() string {
	out, err := exec.CommandContext(context.Background(), "go", "env", "GOPATH").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type nonoProfile struct {
	Extends    []string       `json:"extends,omitempty"`
	Meta       nonoMeta       `json:"meta"`
	Filesystem nonoFilesystem `json:"filesystem"`
}

type nonoMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type nonoFilesystem struct {
	Allow             []string `json:"allow,omitempty"`
	Read              []string `json:"read,omitempty"`
	ReadFile          []string `json:"read_file,omitempty"`
	AllowFile         []string `json:"allow_file,omitempty"`
	UnixSocketSubtree []string `json:"unix_socket_subtree,omitempty"`
	BypassProtection  []string `json:"bypass_protection,omitempty"`
	Deny              []string `json:"deny,omitempty"`
}

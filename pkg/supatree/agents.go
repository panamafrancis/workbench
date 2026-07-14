package supatree

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Agent is one named LLM agent attached to a supatree. Several agents share the
// supatree root directory but resume independently via SessionID.
type Agent struct {
	Name      string    `yaml:"name"`
	Model     string    `yaml:"model"`
	SessionID string    `yaml:"session_id,omitempty"`
	CreatedAt time.Time `yaml:"created_at"`
}

type agentsFile struct {
	Agents []Agent `yaml:"agents"`
}

// LoadAgents reads .supatree/agents.yml, returning an empty list if absent.
func LoadAgents(root string) ([]Agent, error) {
	data, err := os.ReadFile(AgentsPath(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agents: %w", err)
	}
	var f agentsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse agents: %w", err)
	}
	return f.Agents, nil
}

// saveAgents writes .supatree/agents.yml atomically.
func saveAgents(root string, agents []Agent) error {
	if err := os.MkdirAll(StateDir(root), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := yaml.Marshal(agentsFile{Agents: agents})
	if err != nil {
		return fmt.Errorf("marshal agents: %w", err)
	}
	path := AgentsPath(root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write agents: %w", err)
	}
	return os.Rename(tmp, path)
}

// FindAgent returns the agent with the given name, or nil.
func FindAgent(agents []Agent, name string) *Agent {
	for i := range agents {
		if agents[i].Name == name {
			return &agents[i]
		}
	}
	return nil
}

// EnsureAgent returns the named agent for a supatree, creating and persisting it
// with a fresh session ID (via now for the timestamp) if it does not yet exist.
// The bool result reports whether the agent was newly created.
func EnsureAgent(root, name, model string, now time.Time) (Agent, bool, error) {
	agents, err := LoadAgents(root)
	if err != nil {
		return Agent{}, false, err
	}
	if a := FindAgent(agents, name); a != nil {
		return *a, false, nil
	}
	sid, err := newSessionID()
	if err != nil {
		return Agent{}, false, err
	}
	a := Agent{Name: name, Model: model, SessionID: sid, CreatedAt: now}
	agents = append(agents, a)
	if err := saveAgents(root, agents); err != nil {
		return Agent{}, false, err
	}
	return a, true, nil
}

// newSessionID returns a random RFC 4122 version-4 UUID string.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

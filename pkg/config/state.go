package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/panamafrancis/workbench/pkg/git"
)

type State struct {
	LastRunVersion  string    `yaml:"last_run_version"`
	LastUpdateCheck time.Time `yaml:"last_update_check"`
	LatestRelease   string    `yaml:"latest_release"`

	CitiesVisited    []string      `yaml:"cities_visited,omitempty"`
	WorktreesCreated int           `yaml:"worktrees_created,omitempty"`
	WorktreesMerged  int           `yaml:"worktrees_merged,omitempty"`
	Achievements     []Achievement `yaml:"achievements,omitempty"`
	ActivityDays     []string      `yaml:"activity_days,omitempty"`

	// ReservedCities holds worktree names that are still "occupied" and must not
	// be handed out again by name generation — either because a worktree of that
	// name still exists or because its Claude transcript hasn't been cleaned up
	// yet. A name created in one repo must not be reused elsewhere while its
	// Zellij tab / Claude history could still collide. Entries are released by
	// ReclaimReservedCities once both conditions clear.
	ReservedCities []ReservedCity `yaml:"reserved_cities,omitempty"`
}

// ReservedCity pins a generated worktree name and the path whose Claude
// transcript keeps it reserved. Path is recorded so a reclaim pass can tell
// when the history has been cleaned up.
type ReservedCity struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

type Achievement struct {
	ID         string    `yaml:"id"`
	UnlockedAt time.Time `yaml:"unlocked_at"`
}

type AchievementDef struct {
	ID          string
	Description string
	Check       func(s *State) bool
}

var achievementDefs = []AchievementDef{
	{"first-worktree", "First worktree", func(s *State) bool { return s.WorktreesCreated >= 1 }},
	{"10-cities", "10 cities visited", func(s *State) bool { return len(s.CitiesVisited) >= 10 }},
	{"50-cities", "50 cities visited", func(s *State) bool { return len(s.CitiesVisited) >= 50 }},
	{"100-cities", "100 cities visited", func(s *State) bool { return len(s.CitiesVisited) >= 100 }},
	{"first-merge", "First merged PR", func(s *State) bool { return s.WorktreesMerged >= 1 }},
	{"10-merges", "10 merged PRs", func(s *State) bool { return s.WorktreesMerged >= 10 }},
	{"speed-demon", "Cycle time under 1 hour", nil},
	{"3-day-streak", "3-day streak", func(s *State) bool { return s.CurrentStreak() >= 3 }},
	{"7-day-streak", "7-day streak", func(s *State) bool { return s.CurrentStreak() >= 7 }},
	{"30-day-streak", "30-day streak", func(s *State) bool { return s.CurrentStreak() >= 30 }},
}

func AllAchievementDefs() []AchievementDef {
	defs := make([]AchievementDef, len(achievementDefs))
	copy(defs, achievementDefs)
	return defs
}

func AchievementDescription(id string) string {
	for _, d := range achievementDefs {
		if d.ID == id {
			return d.Description
		}
	}
	return id
}

func LoadState() (*State, error) {
	path := StatePath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var s State
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	return &s, nil
}

func (s *State) Save() error {
	path := StatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *State) RecordWorktreeCreated(name string) {
	s.WorktreesCreated++
	if git.IsCityName(name) {
		base := git.ExtractBaseCity(name)
		if !slices.Contains(s.CitiesVisited, base) {
			s.CitiesVisited = append(s.CitiesVisited, base)
		}
	}
	s.recordActivity()
}

// ReserveCity records name (and the worktree path that anchors its Claude
// history) as occupied so future name generation skips it. It is idempotent.
func (s *State) ReserveCity(name, path string) {
	for i := range s.ReservedCities {
		if s.ReservedCities[i].Name == name {
			s.ReservedCities[i].Path = path
			return
		}
	}
	s.ReservedCities = append(s.ReservedCities, ReservedCity{Name: name, Path: path})
}

// ReservedNames returns the names currently held in the reserved-cities cache.
func (s *State) ReservedNames() []string {
	names := make([]string, len(s.ReservedCities))
	for i, rc := range s.ReservedCities {
		names[i] = rc.Name
	}
	return names
}

// ReserveAndReclaim reserves name (anchored at path) and then releases any
// reserved names now safe to reuse. It reads the current config from disk
// itself so every call site behaves identically regardless of how stale the
// caller's in-memory config view is — callers must persist with Save().
func (s *State) ReserveAndReclaim(name, path string, historyExists func(path string) bool) {
	s.ReserveCity(name, path)
	var inConfig map[string]bool
	if cfg, err := Load(); err == nil {
		inConfig = cfg.WorktreeNameSet()
	}
	s.ReclaimReservedCities(inConfig, historyExists)
}

// ReclaimReservedCities releases reserved names that are safe to reuse: the
// worktree no longer exists (its name isn't in inConfig) and its Claude history
// has been cleaned up (historyExists reports false for its path). It reports
// whether anything was released so the caller can decide to persist.
func (s *State) ReclaimReservedCities(inConfig map[string]bool, historyExists func(path string) bool) bool {
	kept := s.ReservedCities[:0]
	changed := false
	for _, rc := range s.ReservedCities {
		if inConfig[rc.Name] || historyExists(rc.Path) {
			kept = append(kept, rc)
		} else {
			changed = true
		}
	}
	s.ReservedCities = kept
	return changed
}

func (s *State) RecordWorktreeMerged() {
	s.WorktreesMerged++
	s.recordActivity()
}

func (s *State) recordActivity() {
	today := time.Now().Format("2006-01-02")
	if len(s.ActivityDays) == 0 || s.ActivityDays[len(s.ActivityDays)-1] != today {
		s.ActivityDays = append(s.ActivityDays, today)
	}
	if len(s.ActivityDays) > 365 {
		s.ActivityDays = s.ActivityDays[len(s.ActivityDays)-365:]
	}
}

func (s *State) CurrentStreak() int {
	if len(s.ActivityDays) == 0 {
		return 0
	}
	today := time.Now().Truncate(24 * time.Hour)
	streak := 0
	for i := len(s.ActivityDays) - 1; i >= 0; i-- {
		day, err := time.Parse("2006-01-02", s.ActivityDays[i])
		if err != nil {
			break
		}
		expected := today.AddDate(0, 0, -streak)
		if day.Equal(expected) {
			streak++
		} else {
			break
		}
	}
	return streak
}

func (s *State) HasAchievement(id string) bool {
	for _, a := range s.Achievements {
		if a.ID == id {
			return true
		}
	}
	return false
}

func (s *State) UnlockAchievement(id string) bool {
	if s.HasAchievement(id) {
		return false
	}
	s.Achievements = append(s.Achievements, Achievement{
		ID:         id,
		UnlockedAt: time.Now(),
	})
	return true
}

func (s *State) CheckAndUnlockAchievements() []Achievement {
	var newly []Achievement
	for _, def := range achievementDefs {
		if def.Check == nil {
			continue
		}
		if s.HasAchievement(def.ID) {
			continue
		}
		if def.Check(s) {
			a := Achievement{ID: def.ID, UnlockedAt: time.Now()}
			s.Achievements = append(s.Achievements, a)
			newly = append(newly, a)
		}
	}
	return newly
}

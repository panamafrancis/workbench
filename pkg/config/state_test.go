package config

import (
	"testing"
	"time"
)

func TestReserveCityAndNames(t *testing.T) {
	s := &State{}
	s.ReserveCity("tokyo", "/wt/tokyo")
	s.ReserveCity("oslo", "/wt/oslo")
	s.ReserveCity("tokyo", "/wt/tokyo-new") // idempotent, updates path
	if len(s.ReservedCities) != 2 {
		t.Fatalf("ReservedCities = %v, want 2 entries", s.ReservedCities)
	}
	if s.ReservedCities[0].Path != "/wt/tokyo-new" {
		t.Errorf("re-reserving tokyo did not update path: %q", s.ReservedCities[0].Path)
	}
	names := s.ReservedNames()
	if len(names) != 2 || names[0] != "tokyo" || names[1] != "oslo" {
		t.Errorf("ReservedNames() = %v, want [tokyo oslo]", names)
	}
}

func TestReclaimReservedCities(t *testing.T) {
	s := &State{}
	s.ReserveCity("inconfig", "/wt/inconfig") // still a live worktree
	s.ReserveCity("history", "/wt/history")   // deleted but transcript remains
	s.ReserveCity("gone", "/wt/gone")         // deleted and cleaned up

	inConfig := map[string]bool{"inconfig": true}
	hasHistory := func(path string) bool { return path == "/wt/history" }

	changed := s.ReclaimReservedCities(inConfig, hasHistory)
	if !changed {
		t.Error("ReclaimReservedCities reported no change, want true")
	}
	names := s.ReservedNames()
	if len(names) != 2 {
		t.Fatalf("ReservedNames() = %v, want [inconfig history]", names)
	}
	for _, n := range names {
		if n == "gone" {
			t.Errorf("%q should have been released", n)
		}
	}

	// A second reclaim with nothing to release must report no change.
	if s.ReclaimReservedCities(inConfig, hasHistory) {
		t.Error("second ReclaimReservedCities reported change, want false")
	}
}

func TestRecordWorktreeCreated(t *testing.T) {
	s := &State{}
	s.RecordWorktreeCreated("tokyo")
	if s.WorktreesCreated != 1 {
		t.Errorf("WorktreesCreated = %d, want 1", s.WorktreesCreated)
	}
	if len(s.CitiesVisited) != 1 || s.CitiesVisited[0] != "tokyo" {
		t.Errorf("CitiesVisited = %v, want [tokyo]", s.CitiesVisited)
	}
	if len(s.ActivityDays) != 1 {
		t.Errorf("ActivityDays = %v, want 1 entry", s.ActivityDays)
	}
}

func TestRecordWorktreeCreatedDuplicateCity(t *testing.T) {
	s := &State{}
	s.RecordWorktreeCreated("tokyo")
	s.RecordWorktreeCreated("tokyo-2")
	if s.WorktreesCreated != 2 {
		t.Errorf("WorktreesCreated = %d, want 2", s.WorktreesCreated)
	}
	if len(s.CitiesVisited) != 1 {
		t.Errorf("CitiesVisited = %v, want 1 entry (tokyo counted once)", s.CitiesVisited)
	}
}

func TestRecordWorktreeCreatedOldFormat(t *testing.T) {
	s := &State{}
	s.RecordWorktreeCreated("bold-atlanta")
	if s.WorktreesCreated != 1 {
		t.Errorf("WorktreesCreated = %d, want 1", s.WorktreesCreated)
	}
	if len(s.CitiesVisited) != 0 {
		t.Errorf("CitiesVisited = %v, want empty (old format not counted)", s.CitiesVisited)
	}
}

func TestRecordWorktreeMerged(t *testing.T) {
	s := &State{}
	s.RecordWorktreeMerged()
	if s.WorktreesMerged != 1 {
		t.Errorf("WorktreesMerged = %d, want 1", s.WorktreesMerged)
	}
	if len(s.ActivityDays) != 1 {
		t.Errorf("ActivityDays = %v, want 1 entry", s.ActivityDays)
	}
}

func TestCurrentStreakEmpty(t *testing.T) {
	s := &State{}
	if s.CurrentStreak() != 0 {
		t.Errorf("CurrentStreak() = %d, want 0", s.CurrentStreak())
	}
}

func TestCurrentStreakConsecutive(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	twoDaysAgo := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	s := &State{
		ActivityDays: []string{twoDaysAgo, yesterday, today},
	}
	if s.CurrentStreak() != 3 {
		t.Errorf("CurrentStreak() = %d, want 3", s.CurrentStreak())
	}
}

func TestCurrentStreakGap(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	threeDaysAgo := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	s := &State{
		ActivityDays: []string{threeDaysAgo, today},
	}
	if s.CurrentStreak() != 1 {
		t.Errorf("CurrentStreak() = %d, want 1", s.CurrentStreak())
	}
}

func TestCurrentStreakYesterdayOnly(t *testing.T) {
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	s := &State{
		ActivityDays: []string{yesterday},
	}
	streak := s.CurrentStreak()
	if streak != 0 {
		t.Errorf("CurrentStreak() = %d, want 0 (no activity today)", streak)
	}
}

func TestCheckAndUnlockAchievements(t *testing.T) {
	s := &State{WorktreesCreated: 1}
	newly := s.CheckAndUnlockAchievements()
	if len(newly) != 1 || newly[0].ID != "first-worktree" {
		t.Errorf("newly = %v, want [first-worktree]", newly)
	}
	if !s.HasAchievement("first-worktree") {
		t.Error("HasAchievement(first-worktree) = false after unlock")
	}

	// Running again should not re-unlock.
	again := s.CheckAndUnlockAchievements()
	if len(again) != 0 {
		t.Errorf("re-check = %v, want empty", again)
	}
}

func TestCheckAndUnlockAchievementsMultiple(t *testing.T) {
	s := &State{
		WorktreesCreated: 1,
		WorktreesMerged:  1,
		CitiesVisited:    make([]string, 10),
	}
	newly := s.CheckAndUnlockAchievements()
	ids := make(map[string]bool)
	for _, a := range newly {
		ids[a.ID] = true
	}
	for _, want := range []string{"first-worktree", "first-merge", "10-cities"} {
		if !ids[want] {
			t.Errorf("expected achievement %q to be unlocked", want)
		}
	}
}

func TestUnlockAchievementManual(t *testing.T) {
	s := &State{}
	ok := s.UnlockAchievement("speed-demon")
	if !ok {
		t.Error("UnlockAchievement returned false for new achievement")
	}
	if !s.HasAchievement("speed-demon") {
		t.Error("HasAchievement(speed-demon) = false after manual unlock")
	}
	ok = s.UnlockAchievement("speed-demon")
	if ok {
		t.Error("UnlockAchievement returned true for duplicate")
	}
}

func TestAchievementDescription(t *testing.T) {
	desc := AchievementDescription("first-worktree")
	if desc != "First worktree" {
		t.Errorf("AchievementDescription(first-worktree) = %q", desc)
	}
	desc = AchievementDescription("unknown")
	if desc != "unknown" {
		t.Errorf("AchievementDescription(unknown) = %q, want fallback", desc)
	}
}

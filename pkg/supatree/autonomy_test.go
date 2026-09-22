package supatree

import (
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestResolveAutonomy(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		meta        *Meta
		wantLevel   Autonomy
		wantOutward bool
	}{
		{name: "nothing configured", wantLevel: AutonomyNudge},
		{name: "workspace default", cfg: Config{DefaultAutonomy: string(AutonomyAuto)}, wantLevel: AutonomyAuto},
		{name: "tree overrides workspace", cfg: Config{DefaultAutonomy: string(AutonomyAuto)},
			meta: &Meta{Autonomy: string(AutonomyOff)}, wantLevel: AutonomyOff},
		{name: "garbage level falls back", cfg: Config{DefaultAutonomy: "yolo"}, wantLevel: AutonomyNudge},
		{name: "garbage tree level falls back to workspace", cfg: Config{DefaultAutonomy: string(AutonomyAuto)},
			meta: &Meta{Autonomy: "yolo"}, wantLevel: AutonomyAuto},
		{name: "outward inherits", cfg: Config{DefaultOutward: true}, wantLevel: AutonomyNudge, wantOutward: true},
		{name: "tree can revoke outward", cfg: Config{DefaultOutward: true},
			meta: &Meta{Outward: boolPtr(false)}, wantLevel: AutonomyNudge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.cfg.Resolve(tc.meta)
			if p.Level != tc.wantLevel {
				t.Errorf("Level = %q, want %q", p.Level, tc.wantLevel)
			}
			if p.Outward != tc.wantOutward {
				t.Errorf("Outward = %v, want %v", p.Outward, tc.wantOutward)
			}
		})
	}
}

// Outward-facing is its own axis: `auto` grants tree creation, not a public
// voice. Wanting the PM to open PRs unattended must not also let it speak in
// your name.
func TestAutoDoesNotImplyOutward(t *testing.T) {
	p := (&Config{DefaultAutonomy: string(AutonomyAuto)}).Resolve(nil)
	if !p.AllowsMutation() {
		t.Fatal("auto should allow mutation")
	}
	if p.Outward {
		t.Error("auto implied outward-facing permission")
	}
}

func TestPermissionGates(t *testing.T) {
	off := (&Config{DefaultAutonomy: string(AutonomyOff)}).Resolve(nil)
	if off.AllowsMessaging() || off.AllowsMutation() {
		t.Error("off should permit neither messaging nor mutation")
	}
	nudge := (&Config{}).Resolve(nil)
	if !nudge.AllowsMessaging() || nudge.AllowsMutation() {
		t.Error("nudge should permit messaging but not mutation")
	}
}

// The level governs what the PM does *unasked*. Below `auto` an explicit
// request is what separates "create a supatree" from "decide to create one",
// and refusing the first is how the default level came to block the most
// ordinary interactive verb there is.
func TestAskedLiftsTheLevel(t *testing.T) {
	nudge := (&Config{}).Resolve(nil)
	if !nudge.AsAsked(true).AllowsMutation() {
		t.Error("an asked-for mutation was refused at nudge")
	}
	if nudge.AllowsMutation() {
		t.Error("AsAsked mutated its receiver")
	}
	// off is report-only, and that is not something asking lifts.
	offAsked := (&Config{DefaultAutonomy: string(AutonomyOff)}).Resolve(nil).AsAsked(true)
	if offAsked.AllowsMutation() {
		t.Error("asking lifted autonomy off")
	}
	// Nobody is in a scheduled turn to have asked, so the claim cannot hold
	// there however the prompt that fired it was worded.
	sched := (&Config{}).Resolve(nil).AsScheduled(false).AsAsked(true)
	if sched.AllowsMutation() {
		t.Error("a scheduled turn honoured an asked-for mutation")
	}
	if d := sched.Deny("creating a supatree"); !strings.Contains(d, "scheduled turn") {
		t.Errorf("scheduled+asked Deny = %q, want it to explain that nobody asked", d)
	}
}

// A turn nobody is watching caps at nudge however the tree is configured,
// unless the schedule entry explicitly opts in.
func TestScheduledCapsAtNudge(t *testing.T) {
	auto := (&Config{DefaultAutonomy: string(AutonomyAuto)}).Resolve(nil)

	capped := auto.AsScheduled(false)
	if capped.AllowsMutation() {
		t.Error("a scheduled turn kept auto without opting in")
	}
	if !capped.AllowsMessaging() {
		t.Error("a scheduled turn should still be able to nudge")
	}
	if opted := auto.AsScheduled(true); !opted.AllowsMutation() {
		t.Error("an opted-in scheduled turn should keep auto")
	}
	// The interactive permission must be unchanged by asking about a scheduled one.
	if !auto.AllowsMutation() {
		t.Error("AsScheduled mutated its receiver")
	}
}

// A denial has to name the setting the human would change, or it is just a no.
func TestDenyNamesTheSetting(t *testing.T) {
	if d := (&Config{}).Resolve(nil).Deny("creating a supatree"); !strings.Contains(d, "autonomy") {
		t.Errorf("Deny = %q, want it to name the setting", d)
	}
	sched := (&Config{DefaultAutonomy: string(AutonomyAuto)}).Resolve(nil).AsScheduled(false)
	if d := sched.Deny("creating a supatree"); !strings.Contains(d, "schedule") {
		t.Errorf("scheduled Deny = %q, want it to explain the scheduled cap", d)
	}
	// A refusal the human's own request would have satisfied has to say so, or
	// the PM reports a config problem instead of asking the obvious question.
	if d := (&Config{}).Resolve(nil).Deny("creating a supatree"); !strings.Contains(d, "asked") {
		t.Errorf("unasked Deny = %q, want it to name the asked flag", d)
	}
}

package supatree

import "fmt"

// Autonomy is how much the PM may do in a supatree without being asked.
//
// It is rendered in the sidebar and settable per tree, because an agent that
// silently pokes your other agents is unsettling the first time it happens and
// you should never have to ask what it is allowed to do.
type Autonomy string

const (
	// AutonomyOff: report only.
	AutonomyOff Autonomy = "off"
	// AutonomyNudge: message agents; never create, delete or push.
	AutonomyNudge Autonomy = "nudge"
	// AutonomyAuto: create trees, open PRs, reap finished trees.
	AutonomyAuto Autonomy = "auto"
)

// DefaultAutonomy is what a supatree gets when nothing says otherwise. `nudge`
// rather than `auto` because a wrong nudge costs a confused agent-turn and a
// wrong `auto` costs a pull request.
const DefaultAutonomy = AutonomyNudge

// ValidAutonomy reports whether s is a level we recognise.
func ValidAutonomy(s string) bool {
	switch Autonomy(s) {
	case AutonomyOff, AutonomyNudge, AutonomyAuto:
		return true
	}
	return false
}

// Permission is what the PM may do in one supatree, resolved from the
// workspace default and the tree's own override.
type Permission struct {
	Level Autonomy
	// Outward allows actions a third party sees: commenting on a pull request,
	// or anything else published in your name.
	//
	// A separate axis on purpose. "Message a local agent" and "comment on a PR"
	// are different kinds of risk — one is private and recoverable, the other is
	// published and permanent — so folding the second into `auto` would mean
	// wanting unattended tree creation also granted a public voice. It defaults
	// off at every level, including `auto`.
	Outward bool
	// Scheduled marks a turn nobody is watching. A scheduled job caps at
	// `nudge` however the tree is configured, unless its entry opts in.
	Scheduled bool
}

// Resolve returns the effective permission for a supatree. meta may be nil for
// an action that has no tree yet.
//
// That case is the reason the workspace default exists: autonomy lives in
// per-tree meta.yml, so without a default nothing governs `new_tree` — the most
// dangerous verb there is — because the tree it would create does not exist to
// carry a level yet.
func (c *Config) Resolve(meta *Meta) Permission {
	p := Permission{Level: DefaultAutonomy}
	if ValidAutonomy(c.DefaultAutonomy) {
		p.Level = Autonomy(c.DefaultAutonomy)
	}
	p.Outward = c.DefaultOutward
	if meta != nil {
		if ValidAutonomy(meta.Autonomy) {
			p.Level = Autonomy(meta.Autonomy)
		}
		if meta.Outward != nil {
			p.Outward = *meta.Outward
		}
	}
	return p
}

// Scheduled returns p as it applies to a turn nobody is watching.
func (p Permission) AsScheduled(optIn bool) Permission {
	p.Scheduled = true
	if !optIn && p.Level == AutonomyAuto {
		p.Level = AutonomyNudge
	}
	return p
}

// AllowsMessaging reports whether the PM may nudge agents.
func (p Permission) AllowsMessaging() bool { return p.Level != AutonomyOff }

// AllowsMutation reports whether the PM may create, remove or push.
func (p Permission) AllowsMutation() bool { return p.Level == AutonomyAuto }

// Deny renders why an action is not permitted, in terms of the setting the
// human would change to permit it.
func (p Permission) Deny(action string) string {
	where := "`default_autonomy` in ~/.supatree/config.yml"
	if p.Scheduled {
		return fmt.Sprintf("%s is not permitted on a scheduled turn (autonomy %s; a scheduled job caps at nudge unless its schedule entry sets autonomy: auto)", action, p.Level)
	}
	return fmt.Sprintf("%s is not permitted at autonomy %q — raise the tree's `autonomy` in .supatree/meta.yml, or %s", action, p.Level, where)
}

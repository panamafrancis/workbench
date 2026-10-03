package supatree

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Tree operations a sandboxed process asks the watcher to do for it.
//
// Creating, removing and syncing a tree write member worktrees under repos/,
// the base clones' git dirs and the stack's — none of which the PM is granted,
// and only one tree's worth of which a coding agent is. So the MCP tools do
// not do these themselves: they write a request into a directory they own and
// the watcher, which runs outside the sandbox, carries it out and writes the
// result back. It is the same shape as the launch queue, with an answer.
//
// Where a request is written is what authorizes it. The PM's own home may ask
// for anything; a tree's state directory may only ask to sync that tree.

// OpKind names a tree operation.
type OpKind string

const (
	OpNew    OpKind = "new"    // create a tree from a stack
	OpReview OpKind = "review" // create a review tree for pull requests
	OpRemove OpKind = "remove" // remove a tree
	OpSync   OpKind = "sync"   // reconcile a tree's members with its spec
)

// Op is one request.
type Op struct {
	ID     string    `json:"id"`
	At     time.Time `json:"at"`
	Kind   OpKind    `json:"kind"`
	Stack  string    `json:"stack,omitempty"`
	Name   string    `json:"name,omitempty"`
	Intent string    `json:"intent,omitempty"`
	PRs    []string  `json:"prs,omitempty"`
	Tree   string    `json:"tree,omitempty"`
	Force  bool      `json:"force,omitempty"`
	Prune  bool      `json:"prune,omitempty"`
}

// OpResult is the watcher's answer.
type OpResult struct {
	ID   string    `json:"id"`
	Done time.Time `json:"done"`
	// Tree is the tree the operation acted on — for a create, the name it got.
	Tree string `json:"tree,omitempty"`
	Text string `json:"text"`
	Err  string `json:"err,omitempty"`
}

// OpsDir is where requests from dir's owner are queued.
func OpsDir(dir string) string {
	return filepath.Join(dir, "ops")
}

const (
	opRequestExt = ".json"
	opRunningExt = ".running"
	opResultExt  = ".result"
)

// opPoll is how often a waiting tool looks for its answer. Cheap: one stat.
const opPoll = 200 * time.Millisecond

// ErrOpNotPickedUp is returned when nothing claimed a request in time.
var ErrOpNotPickedUp = errors.New("the watcher did not pick this up — is `supatree watch` running? `supatree start` spawns it; " +
	"until it is, ask the human to run the command themselves")

// RequestOp queues op in dir's ops queue and waits up to timeout for the
// watcher's answer.
//
// A request nobody claimed by the deadline is withdrawn, so it cannot run
// later behind the caller's back. One already running is left to finish; the
// caller is told so rather than told it failed.
func RequestOp(dir string, op Op, timeout time.Duration) (*OpResult, error) {
	if op.ID == "" {
		op.ID = newOpID()
	}
	if op.At.IsZero() {
		op.At = time.Now().UTC()
	}
	qdir := OpsDir(dir)
	if err := writeJSONAtomic(filepath.Join(qdir, op.ID+opRequestExt), op); err != nil {
		return nil, fmt.Errorf("queue %s: %w", op.Kind, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		if res, ok := takeResult(qdir, op.ID); ok {
			return res, nil
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(opPoll)
	}
	req := filepath.Join(qdir, op.ID+opRequestExt)
	if err := os.Remove(req); err == nil {
		return nil, ErrOpNotPickedUp
	}
	if res, ok := takeResult(qdir, op.ID); ok {
		return res, nil
	}
	return nil, fmt.Errorf("%s is still running in the watcher after %s; check again shortly", op.Kind, timeout)
}

func takeResult(qdir, id string) (*OpResult, bool) {
	path := filepath.Join(qdir, id+opResultExt)
	var res OpResult
	if err := readJSON(path, &res); err != nil {
		return nil, false
	}
	_ = os.Remove(path)
	return &res, true
}

func newOpID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

// ClaimedOp is a request the watcher has taken and must answer.
type ClaimedOp struct {
	Dir string // the requester's directory (PMDir or a tree's state dir)
	Op  Op
}

// ClaimOps takes every waiting request from dirs, oldest first. Claiming is a
// rename, so a request is run once even if two watchers ever raced, and a
// requester that gave up and removed its request is never run.
func ClaimOps(dirs []string) []ClaimedOp {
	var out []ClaimedOp
	for _, dir := range dirs {
		qdir := OpsDir(dir)
		entries, err := os.ReadDir(qdir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, opRequestExt) {
				continue
			}
			id := strings.TrimSuffix(name, opRequestExt)
			running := filepath.Join(qdir, id+opRunningExt)
			if err := os.Rename(filepath.Join(qdir, name), running); err != nil {
				continue
			}
			var op Op
			if err := readJSON(running, &op); err != nil || op.ID != id {
				_ = os.Remove(running)
				continue
			}
			out = append(out, ClaimedOp{Dir: dir, Op: op})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Op.At.Before(out[j].Op.At) })
	return out
}

// AnswerOp writes the result for a claimed request and clears its claim.
func AnswerOp(dir string, res OpResult) error {
	qdir := OpsDir(dir)
	res.Done = time.Now().UTC()
	if err := writeJSONAtomic(filepath.Join(qdir, res.ID+opResultExt), res); err != nil {
		return err
	}
	return os.Remove(filepath.Join(qdir, res.ID+opRunningExt))
}

// AbandonInterrupted answers every request a previous watcher claimed and
// never finished. Re-running one could create a tree twice, so it is reported
// as interrupted instead and the requester decides.
func AbandonInterrupted(dirs []string) {
	for _, dir := range dirs {
		qdir := OpsDir(dir)
		entries, err := os.ReadDir(qdir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if id, ok := strings.CutSuffix(e.Name(), opRunningExt); ok {
				_ = AnswerOp(dir, OpResult{ID: id, Err: "interrupted: the watcher stopped while this was running; check list_trees before retrying"})
			}
		}
	}
}

// OpSources lists every directory requests may come from: the PM's home and
// each tree's state.
func OpSources(insts []*Instance) []string {
	dirs := make([]string, 0, 1+len(insts))
	dirs = append(dirs, PMDir())
	for _, inst := range insts {
		dirs = append(dirs, StateDir(inst.Root))
	}
	return dirs
}

// OpHooks lets the caller act on what an operation did with things this
// package cannot reach (zellij tabs and layouts).
type OpHooks struct {
	// Removed is called after a tree has been removed.
	Removed func(tree string, res *RemoveResult)
}

// RunOp carries out a claimed request. It never panics on bad input; every
// failure is an answer.
func (c *Config) RunOp(claimed ClaimedOp, hooks OpHooks) OpResult {
	op := claimed.Op
	res := OpResult{ID: op.ID}
	if err := c.authorizeOp(claimed); err != nil {
		res.Err = err.Error()
		return res
	}
	var err error
	switch op.Kind {
	case OpNew:
		err = c.runNew(op, &res)
	case OpReview:
		err = c.runReview(op, &res)
	case OpRemove:
		err = c.runRemove(op, &res, hooks)
	case OpSync:
		err = c.runSync(op, &res)
	default:
		err = fmt.Errorf("unknown operation %q", op.Kind)
	}
	if err != nil {
		res.Err = err.Error()
	}
	return res
}

// authorizeOp enforces that where a request was written decides what it may
// ask for: the PM's home anything, a tree's own state only a sync of itself.
func (c *Config) authorizeOp(claimed ClaimedOp) error {
	if filepath.Clean(claimed.Dir) == PMDir() {
		return nil
	}
	if claimed.Op.Kind != OpSync {
		return fmt.Errorf("%s may only be requested by the PM", claimed.Op.Kind)
	}
	inst, err := Get(c, claimed.Op.Tree)
	if err != nil {
		return err
	}
	if filepath.Clean(StateDir(inst.Root)) != filepath.Clean(claimed.Dir) {
		return fmt.Errorf("a tree may only sync itself")
	}
	return nil
}

func (c *Config) runNew(op Op, res *OpResult) error {
	inst, report, err := New(c, CreateOptions{Stack: op.Stack, Name: op.Name, Intent: op.Intent})
	if err != nil {
		return err
	}
	res.Tree = inst.Name
	res.Text = fmt.Sprintf("created supatree %q (%d members).", inst.Name, len(inst.Members)) + reportWarnings(report)
	return nil
}

func (c *Config) runReview(op Op, res *OpResult) error {
	refs, err := ResolvePRRefs(op.PRs)
	if err != nil {
		return err
	}
	inst, report, err := NewReview(c, ReviewOptions{Stack: op.Stack, Name: op.Name, Intent: op.Intent, PRs: refs})
	if err != nil {
		return err
	}
	res.Tree = inst.Name
	var b strings.Builder
	fmt.Fprintf(&b, "created review tree %q (%d members).\n", inst.Name, len(inst.Members))
	for _, m := range inst.Members {
		if m.Review != nil {
			fmt.Fprintf(&b, "  %s at %s#%d\n", m.Alias, m.Review.Repo, m.Review.Number)
		}
	}
	b.WriteString("\nThe authoring commands are refused there, and it reports `reviewing` rather than a ship state.")
	b.WriteString(reportWarnings(report))
	res.Text = b.String()
	return nil
}

func (c *Config) runRemove(op Op, res *OpResult, hooks OpHooks) error {
	removed, err := Remove(c, op.Tree, RemoveOptions{Force: op.Force})
	if err != nil {
		return err
	}
	if hooks.Removed != nil {
		hooks.Removed(op.Tree, removed)
	}
	res.Tree = op.Tree
	res.Text = "removed supatree " + op.Tree
	if len(removed.Warnings) > 0 {
		res.Text += "\nwarnings: " + strings.Join(removed.Warnings, "; ")
	}
	return nil
}

func (c *Config) runSync(op Op, res *OpResult) error {
	inst, err := Get(c, op.Tree)
	if err != nil {
		return err
	}
	report, err := Sync(c, inst.Root, op.Prune)
	res.Tree = op.Tree
	res.Text = FormatSyncReport(report)
	return err
}

// FormatSyncReport renders what a sync did, and what it means for the agents
// already running there.
func FormatSyncReport(report *SyncReport) string {
	if report == nil {
		return ""
	}
	var b strings.Builder
	for _, a := range report.Created {
		fmt.Fprintf(&b, "+ %s\n", a)
	}
	for _, a := range report.Pruned {
		fmt.Fprintf(&b, "- %s\n", a)
	}
	for _, w := range report.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	if len(report.Created) > 0 {
		fmt.Fprintf(&b, "Agents already running here cannot commit in %s until they restart: "+
			"a sandbox's reach is fixed when it starts.\n", strings.Join(report.Created, ", "))
	}
	if b.Len() == 0 {
		return "already in sync"
	}
	return strings.TrimRight(b.String(), "\n")
}

func reportWarnings(report *SyncReport) string {
	if report == nil || len(report.Warnings) == 0 {
		return ""
	}
	return "\nwarnings: " + strings.Join(report.Warnings, "; ")
}

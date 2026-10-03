package supatree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// A request is answered once, by whoever claims it, and the requester gets the
// answer and nothing is left behind.
func TestOpRoundTrip(t *testing.T) {
	testutil.IsolateHome(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			for _, c := range ClaimOps([]string{PMDir()}) {
				_ = AnswerOp(c.Dir, OpResult{ID: c.Op.ID, Tree: treeLima, Text: "did " + string(c.Op.Kind)})
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	res, err := RequestOp(PMDir(), Op{Kind: OpNew, Stack: "s"}, 5*time.Second)
	<-done
	if err != nil {
		t.Fatalf("RequestOp: %v", err)
	}
	if res.Tree != treeLima || res.Text != "did new" {
		t.Errorf("result = %+v", res)
	}
	entries, _ := os.ReadDir(OpsDir(PMDir()))
	if len(entries) != 0 {
		t.Errorf("queue not empty after the answer was taken: %v", entries)
	}
}

// Nobody listening: the request is withdrawn, so it cannot run later behind
// the caller's back, and the error says what to check.
func TestOpNotPickedUpIsWithdrawn(t *testing.T) {
	testutil.IsolateHome(t)
	_, err := RequestOp(PMDir(), Op{Kind: OpRemove, Tree: treeLima}, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "supatree watch") {
		t.Fatalf("err = %v, want one naming the watcher", err)
	}
	if claimed := ClaimOps([]string{PMDir()}); len(claimed) != 0 {
		t.Errorf("a withdrawn request was still claimable: %+v", claimed)
	}
}

// Where a request was written decides what it may ask for: a tree's own state
// may ask only to sync that tree.
func TestOpAuthorization(t *testing.T) {
	testutil.IsolateHome(t)
	base := t.TempDir()
	root := filepath.Join(base, treeA)
	if err := (&Meta{Name: treeA, Root: root}).Save(root); err != nil {
		t.Fatal(err)
	}
	if err := SaveSpec(root, &Spec{}); err != nil {
		t.Fatal(err)
	}
	c := &Config{TreesBase: base}
	treeDir := StateDir(root)

	cases := []struct {
		dir  string
		op   Op
		deny bool
	}{
		{PMDir(), Op{Kind: OpRemove, Tree: treeA}, false},
		{treeDir, Op{Kind: OpSync, Tree: treeA}, false},
		{treeDir, Op{Kind: OpNew, Stack: "s"}, true},
		{treeDir, Op{Kind: OpRemove, Tree: treeA}, true},
	}
	for _, tc := range cases {
		err := c.authorizeOp(ClaimedOp{Dir: tc.dir, Op: tc.op})
		if (err != nil) != tc.deny {
			t.Errorf("%s from %s: err = %v, want deny=%v", tc.op.Kind, tc.dir, err, tc.deny)
		}
	}

	// Another tree's state dir may not sync this one.
	other := filepath.Join(base, treeB)
	if err := (&Meta{Name: treeB, Root: other}).Save(other); err != nil {
		t.Fatal(err)
	}
	if err := c.authorizeOp(ClaimedOp{Dir: StateDir(other), Op: Op{Kind: OpSync, Tree: treeA}}); err == nil {
		t.Error("a tree was allowed to sync a different tree")
	}
}

// A claim left by a watcher that died is answered as interrupted rather than
// re-run, which could create a tree twice.
func TestAbandonInterrupted(t *testing.T) {
	testutil.IsolateHome(t)
	qdir := OpsDir(PMDir())
	if err := writeJSONAtomic(filepath.Join(qdir, "x"+opRunningExt), Op{ID: "x", Kind: OpNew}); err != nil {
		t.Fatal(err)
	}
	AbandonInterrupted([]string{PMDir()})
	res, ok := takeResult(qdir, "x")
	if !ok || !strings.Contains(res.Err, "interrupted") {
		t.Errorf("result = %+v, %v", res, ok)
	}
}

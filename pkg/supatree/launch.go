package supatree

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
)

// KickoffPrompt is the first message an agent is launched with when mail is
// waiting for it. It is fixed text on purpose: the brief itself travels through
// the mailbox, which every model can read and which survives a restart, and
// nothing a PM (or a PR description it relayed) wrote ever reaches a command
// line or a layout file.
const KickoffPrompt = "You have been started with messages waiting. Read .supatree/info.md, " +
	"then call the `inbox` tool and carry out what it asks, end to end, without waiting for further input."

// PMAgentName is the name a tree agent uses to address the PM. The PM is in no
// tree's agents.yml, so message_agent special-cases it, and the watcher
// forwards what lands in that mailbox into the PM's request queue.
const PMAgentName = "pm"

// LaunchRequest asks the watcher to open an agent's tab. The PM cannot do it
// itself: it runs inside nono, and only the watcher runs outside.
type LaunchRequest struct {
	At    time.Time `json:"at"`
	Tree  string    `json:"tree"`
	Agent string    `json:"agent"`
	// Session is the zellij session to open the tab in — the PM's own, read
	// from ZELLIJ_SESSION_NAME. Empty falls back to the watcher's --session.
	Session string `json:"session,omitempty"`
	From    string `json:"from,omitempty"`
}

// QueueLaunch appends a launch request for the watcher.
func QueueLaunch(r LaunchRequest) error {
	if r.Tree == "" {
		return fmt.Errorf("launch request has no tree")
	}
	if r.Agent == "" {
		r.Agent = MainAgent
	}
	if err := ValidateAgentName(r.Agent); err != nil {
		return err
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	return config.WithFileLock(LaunchLockPath(), func() error {
		return appendJSONL(LaunchPath(), []LaunchRequest{r})
	})
}

// DrainLaunches takes every queued launch request and empties the queue. A
// torn line costs that request only.
func DrainLaunches() ([]LaunchRequest, error) {
	var out []LaunchRequest
	err := config.WithFileLock(LaunchLockPath(), func() error {
		data, err := os.ReadFile(LaunchPath())
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var r LaunchRequest
			if err := json.Unmarshal([]byte(line), &r); err != nil || r.Tree == "" {
				continue
			}
			out = append(out, r)
		}
		return os.Remove(LaunchPath())
	})
	return out, err
}

// HasMail reports whether an agent has anything waiting. An unreadable mailbox
// reads as empty: the cost is an agent that starts idle, as it always used to.
func HasMail(root, agent string) bool {
	msgs, err := Mail(root, agent)
	return err == nil && len(msgs) > 0
}

// ForwardPMMail moves what tree agents have left for the PM into the PM's
// request queue, and reports how many it moved.
//
// A tree agent is sandboxed to its own tree, so the one place it can leave the
// PM a message is that tree's mailbox — which the PM never looks in unprompted.
// The request queue is what the PM reads first every turn, so that is where a
// reply has to end up. Only the watcher can write both.
func ForwardPMMail(insts []*Instance) (int, error) {
	n := 0
	var firstErr error
	for _, inst := range insts {
		msgs, err := Mail(inst.Root, PMAgentName)
		if err != nil || len(msgs) == 0 {
			continue
		}
		for _, m := range msgs {
			req := Request{At: m.At, From: m.From, Tree: inst.Name, Text: m.Text}
			if err := AppendRequest(req); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			// Removed only once queued: a failed append leaves the message to
			// be forwarded next round rather than lost.
			_ = os.Remove(m.Path)
			n++
		}
	}
	return n, firstErr
}

// DefaultReviewBrief is what a review tree's agent is told to do when the PM
// starts it without a brief of its own. It is the hands-off version of the
// review craft in `docs topic: review`: do the whole review, leave it where
// the human and the PM can both read it, and say when it is done.
func DefaultReviewBrief(inst *Instance) string {
	var b strings.Builder
	b.WriteString("Review the pull requests checked out in this tree, end to end, without waiting for further input.\n\n")
	for _, m := range inst.Members {
		if m.Review != nil {
			fmt.Fprintf(&b, "- %s: %s\n", m.Alias, m.Review.URL)
		}
	}
	b.WriteString(`
1. Call ` + "`docs`" + ` with topic ` + "`review`" + ` and follow it.
2. Call ` + "`pr_comments`" + ` for each repo so you do not repeat what reviewers already said.
3. Build, lint and test each repo here, and do the cross-repo check.
4. Write the full review to .supatree/review.md: one section per pull request, blocking findings separated from non-blocking ones, every line number taken from the pull request head.
5. Post one batched review per pull request with ` + "`review_post`" + `: APPROVE if nothing blocks, REQUEST_CHANGES if something does. If it is refused, leave the review in .supatree/review.md for the human to post.
6. When you are done, call ` + "`message_agent`" + ` with agent "pm" and a three-line summary: the verdict per pull request and where the review is.
`)
	return b.String()
}

package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/git"
)

// ValidateAgentName rejects a name that must not become a path segment.
//
// Delivery is deliberately not gated on the agent existing — a message left for
// one that has not been launched yet is waiting when it starts — so the name
// reaches filepath.Join unchecked by anything else, and "../../.." would write
// outside the supatree entirely. It is the same charset EnsureAgent enforces.
func ValidateAgentName(agent string) error {
	if err := git.ValidateName(agent, nil); err != nil {
		return fmt.Errorf("invalid agent name %q: %w", agent, err)
	}
	return nil
}

// MailDir is an agent's inbox inside a supatree.
//
// The mailbox is the *contract* for agent-to-agent messaging, and the message
// bus is only an optimisation on top of it. That ordering is deliberate: a file
// works for every model, survives a restart, and can be written by a Go process
// — none of which is true of the bus, which exists only for Claude and only
// from inside another agent. Losing the bus costs latency; losing the mailbox
// would lose the message.
func MailDir(root, agent string) string {
	return filepath.Join(StateDir(root), "mail", agent)
}

// Message is one delivery sitting in an agent's inbox.
type Message struct {
	At   time.Time `json:"at"`
	From string    `json:"from"`
	Text string    `json:"text"`
	// Path is where this message was read from, so Drain's caller can report
	// what it consumed.
	Path string `json:"-"`
}

// mailLockPath serializes delivery to one agent's inbox.
func mailLockPath(root, agent string) string {
	return MailDir(root, agent) + ".lock"
}

// Deliver appends a message to an agent's inbox. It is the universal half of
// "send a message to an agent": every model can read a file, so this always
// works, whether or not the recipient is running.
//
// One file per message rather than one appended log, because delivery and
// consumption are separate processes: a reader that has taken a message deletes
// exactly that file, with no read-modify-write of a shared log to lose a
// concurrent delivery.
func Deliver(root, agent, from, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("refusing to deliver an empty message")
	}
	if err := ValidateAgentName(agent); err != nil {
		return err
	}
	dir := MailDir(root, agent)
	return config.WithFileLock(mailLockPath(root, agent), func() error {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create mailbox: %w", err)
		}
		msg := Message{At: time.Now().UTC(), From: from, Text: text}
		// Nanosecond precision plus the lock makes the name both ordered and
		// unique: two deliveries in the same nanosecond cannot happen while one
		// holds the lock.
		name := fmt.Sprintf("%d.json", msg.At.UnixNano())
		return writeJSONAtomic(filepath.Join(dir, name), msg)
	})
}

// Mail returns an agent's pending messages, oldest first, without consuming
// them. Use it to show an inbox; use Drain to actually take delivery.
func Mail(root, agent string) ([]Message, error) {
	return readMail(root, agent, false)
}

// Drain returns an agent's pending messages and removes them, so each message
// is acted on once. Taking and deleting happen under the delivery lock, so a
// message arriving mid-drain is either fully written and taken, or left for the
// next drain — never half-read and then deleted.
func Drain(root, agent string) ([]Message, error) {
	return readMail(root, agent, true)
}

func readMail(root, agent string, consume bool) ([]Message, error) {
	if err := ValidateAgentName(agent); err != nil {
		return nil, err
	}
	dir := MailDir(root, agent)
	var msgs []Message
	err := config.WithFileLock(mailLockPath(root, agent), func() error {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read mailbox: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			var msg Message
			if err := readJSON(path, &msg); err != nil {
				// A torn message must not block the inbox forever. Drop it on a
				// consuming read; leave it alone otherwise.
				if consume {
					_ = os.Remove(path)
				}
				continue
			}
			msg.Path = path
			msgs = append(msgs, msg)
		}
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].At.Before(msgs[j].At) })
		if consume {
			for _, m := range msgs {
				_ = os.Remove(m.Path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

// FormatMail renders an inbox for an agent to read.
func FormatMail(msgs []Message) string {
	if len(msgs) == 0 {
		return "no messages"
	}
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s] from %s:\n%s\n\n", m.At.Format(time.RFC3339), m.From, strings.TrimSpace(m.Text))
	}
	return strings.TrimSpace(b.String())
}

// MailboxCount reports how many messages are waiting for an agent, without
// consuming them. Cheap enough for a UI to call on a tick.
func MailboxCount(root, agent string) int {
	entries, err := os.ReadDir(MailDir(root, agent))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

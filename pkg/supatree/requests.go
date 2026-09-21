package supatree

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
)

// Request is something asking for the PM's attention. Anything that can append
// to a file can raise one — the sidebar, the watcher, the scheduler, a shell —
// which is the point: a Go process cannot use a message bus, but every Go
// process can append a line.
type Request struct {
	At     time.Time `json:"at"`
	From   string    `json:"from"`
	Tree   string    `json:"tree,omitempty"`
	Member string    `json:"member,omitempty"`
	PR     int       `json:"pr,omitempty"`
	Text   string    `json:"text"`
}

// AppendRequest adds a request to the queue.
func AppendRequest(r Request) error {
	if strings.TrimSpace(r.Text) == "" {
		return fmt.Errorf("refusing to queue an empty request")
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	return config.WithFileLock(RequestsLockPath(), func() error {
		return appendJSONL(RequestsPath(), []Request{r})
	})
}

// readOffset is where the PM has read up to, as a byte offset into the queue
// plus the file size it was valid for.
type readOffset struct {
	Offset int64 `json:"offset"`
	Size   int64 `json:"size"`
}

// PendingRequests returns every request the PM has not yet read, and the offset
// to commit once it has acted on them.
//
// The offset is bytes rather than a timestamp because it is exact: two requests
// raised in the same nanosecond are two entries, and a timestamp cursor would
// silently drop one. It is stored with the file size it was taken at, so a
// truncated or rotated queue is detected (size < offset) and re-read from the
// start rather than seeking past the end — losing the backlog is the failure
// this whole design exists to avoid.
func PendingRequests() ([]Request, int64, error) {
	off := loadOffset()
	f, err := os.Open(RequestsPath())
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("open requests: %w", err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("stat requests: %w", err)
	}
	// The queue only ever grows, so a file shorter than it was when the offset
	// was committed has been truncated or rotated and the offset means nothing.
	// Both comparisons matter: Size catches a rotation that has since regrown
	// past the old offset, which Offset alone would seek straight past.
	start := off.Offset
	if info.Size() < off.Offset || info.Size() < off.Size {
		start = 0
	}
	if _, err := f.Seek(start, 0); err != nil {
		return nil, 0, fmt.Errorf("seek requests: %w", err)
	}

	var out []Request
	pos := start
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		pos += int64(len(line)) + 1 // the newline the scanner stripped
		if len(line) == 0 {
			continue
		}
		var r Request
		if err := json.Unmarshal(line, &r); err != nil {
			// A torn line costs that line, not the backlog behind it.
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("read requests: %w", err)
	}
	return out, pos, nil
}

// CommitRequests records that everything up to offset has been read. Call it
// *after* acting, so a PM that dies mid-turn re-reads rather than loses.
func CommitRequests(offset int64) error {
	info, err := os.Stat(RequestsPath())
	size := int64(0)
	if err == nil {
		size = info.Size()
	}
	return writeJSONAtomic(PMOffsetPath(), readOffset{Offset: offset, Size: size})
}

func loadOffset() readOffset {
	var off readOffset
	if err := readJSON(PMOffsetPath(), &off); err != nil {
		return readOffset{}
	}
	return off
}

// FormatRequests renders the queue for the PM to act on.
func FormatRequests(reqs []Request) string {
	if len(reqs) == 0 {
		return "no pending requests"
	}
	var b strings.Builder
	for _, r := range reqs {
		fmt.Fprintf(&b, "[%s] from %s", r.At.Format(time.RFC3339), r.From)
		if r.Tree != "" {
			fmt.Fprintf(&b, " · %s", r.Tree)
			if r.Member != "" {
				fmt.Fprintf(&b, "/%s", r.Member)
			}
			if r.PR > 0 {
				fmt.Fprintf(&b, " #%d", r.PR)
			}
		}
		fmt.Fprintf(&b, "\n%s\n\n", strings.TrimSpace(r.Text))
	}
	return strings.TrimSpace(b.String())
}

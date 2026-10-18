package sandbox

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Codex keeps one transcript ("rollout") per session under
// $CODEX_HOME/sessions/YYYY/MM/DD/rollout-<local time>-<session id>.jsonl. The
// first line is a session_meta record carrying the session's id, its start
// time and the directory it ran in. Older rollouts may be compressed
// (.jsonl.zst); those are skipped — they are archived, not resumable here.
//
// Neither the file name nor the date directories say which directory a
// session ran in, so finding "the session that ran here" means reading
// first lines.

// codexHome is $CODEX_HOME when set to an absolute path, else ~/.codex.
func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); filepath.IsAbs(h) {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// codexSessionLen is the length of a canonical UUID, the tail of a rollout name.
const codexSessionLen = 36

// codexRolloutID returns the session id a rollout file name ends in.
func codexRolloutID(name string) (string, bool) {
	if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
		return "", false
	}
	base := strings.TrimSuffix(name, ".jsonl")
	if len(base) < len("rollout-")+codexSessionLen+1 {
		return "", false
	}
	id := base[len(base)-codexSessionLen:]
	if base[len(base)-codexSessionLen-1] != '-' || !looksLikeUUID(id) {
		return "", false
	}
	return id, true
}

func looksLikeUUID(s string) bool {
	if len(s) != codexSessionLen {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

// walkCodexRollouts calls fn for every rollout under the sessions directory.
// A missing or unreadable directory is no rollouts. fn returns false to stop.
func walkCodexRollouts(fn func(path, id string, d fs.DirEntry) bool) {
	home := codexHome()
	if home == "" {
		return
	}
	_ = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subdirectory hides only its own rollouts.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		id, ok := codexRolloutID(d.Name())
		if !ok {
			return nil
		}
		if !fn(path, id, d) {
			return fs.SkipAll
		}
		return nil
	})
}

// codexTranscript is the rollout of sessionID that ran in dir, or "" when
// there is none. Every codex sandbox can write under $CODEX_HOME, so a rollout
// is only trusted as this directory's when its own record says it ran here
// and names the same session as its file name.
func codexTranscript(dir, sessionID string) string {
	if !looksLikeUUID(sessionID) {
		return ""
	}
	var found string
	walkCodexRollouts(func(path, id string, _ fs.DirEntry) bool {
		if !strings.EqualFold(id, sessionID) {
			return true
		}
		if m, ok := readCodexMeta(path); ok && strings.EqualFold(m.ID, id) && sameDir(m.Cwd, dir) {
			found = path
		}
		return false
	})
	return found
}

// codexMeta is what a rollout's first line says about its session.
type codexMeta struct {
	ID    string
	Cwd   string
	Start time.Time
}

// maxCodexMetaLine bounds the first-line read: session_meta carries the base
// instructions, so it runs to tens of kilobytes, but it is not unbounded.
const maxCodexMetaLine = 4 << 20

// readCodexMeta parses a rollout's session_meta line.
func readCodexMeta(path string) (codexMeta, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexMeta{}, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxCodexMetaLine)
	if !sc.Scan() {
		return codexMeta{}, false
	}
	var line struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			ID        string `json:"id"`
			Timestamp string `json:"timestamp"`
			Cwd       string `json:"cwd"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(sc.Bytes(), &line); err != nil || line.Type != "session_meta" {
		return codexMeta{}, false
	}
	m := codexMeta{ID: line.Payload.ID, Cwd: line.Payload.Cwd}
	for _, ts := range []string{line.Payload.Timestamp, line.Timestamp} {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			m.Start = t
			break
		}
	}
	return m, m.Cwd != ""
}

// sameDir reports whether two paths name the same directory, through
// symlinks (/tmp is /private/tmp on darwin, and a CLI records whichever its
// getcwd returned).
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// codexSessionIn reports whether any codex session ran in dir.
func codexSessionIn(dir string) bool {
	found := false
	walkCodexRollouts(func(path, _ string, _ fs.DirEntry) bool {
		if m, ok := readCodexMeta(path); ok && sameDir(m.Cwd, dir) {
			found = true
			return false
		}
		return true
	})
	return found
}

// discoverSlack absorbs the gap between a launcher's clock reading and the
// start time the CLI records a moment later (and second-granular stamps).
const discoverSlack = 5 * time.Second

// discoverCodexSession finds the earliest codex session that started in dir
// at or after since and is not in claimed.
func discoverCodexSession(dir string, since time.Time, claimed map[string]bool) (string, bool) {
	floor := since.Add(-discoverSlack)
	var best codexMeta
	walkCodexRollouts(func(path, id string, d fs.DirEntry) bool {
		// A rollout is written as its session runs, so one modified before
		// the launch cannot be the launch's.
		if info, err := d.Info(); err != nil || info.ModTime().Before(floor) {
			return true
		}
		m, ok := readCodexMeta(path)
		// The id ends up on a command line (resume <id>): only a UUID that
		// the file name and the record agree on is taken.
		if !ok || !strings.EqualFold(m.ID, id) || !sameDir(m.Cwd, dir) {
			return true
		}
		m.ID = id
		if claimed[m.ID] || m.Start.IsZero() || m.Start.Before(floor) {
			return true
		}
		if best.ID == "" || m.Start.Before(best.Start) {
			best = m
		}
		return true
	})
	return best.ID, best.ID != ""
}

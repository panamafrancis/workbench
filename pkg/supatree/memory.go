package supatree

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
)

// NotesDirName is the curated memory inside a stack repo.
//
// It is in the stack repo rather than in a store of our own because that repo
// already has every property a memory wants and an embedding index does not:
// it is diffable, blameable, reviewable, shared with the team, and readable by
// `supatree` itself. Curation arrives as a pull request instead of a migration.
const NotesDirName = "notes"

// NotesDir is the curated memory directory for a stack repo.
func NotesDir(stackPath string) string {
	return filepath.Join(stackPath, NotesDirName)
}

// notesReadme is scaffolded alongside AGENTS.md. Without it `notes/` is an
// empty directory nobody writes to and everybody forgets about.
const notesReadme = `# Notes

Durable memory for this stack, kept in git so it is reviewed like code.

Write here what would otherwise have to be rediscovered: how this system fits
together, what a past change turned out to cost, which approaches were tried and
abandoned and why.

Two rules keep it worth reading.

**Facts about the past, not about the code.** "The migration in #412 needed a
backfill nobody expected" stays true forever. "AuthService lives in pkg/auth"
is true until someone moves it, and a memory that quietly goes wrong is worse
than no memory.

**Nothing that git already answers.** Who changed what, when, and in which
order is in the history. Notes are for the reasoning that is not.
`

// ScaffoldNotes creates the notes directory in a stack repo.
func ScaffoldNotes(stackPath string) error {
	dir := NotesDir(stackPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create notes dir: %w", err)
	}
	path := filepath.Join(dir, "README.md")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, []byte(notesReadme), 0644); err != nil {
		return fmt.Errorf("write notes README: %w", err)
	}
	return nil
}

// HistoryEntry is one finished piece of work, reconstructed from the ledger and
// the PR cache rather than from anything written for the purpose.
type HistoryEntry struct {
	Tree   string
	Repos  []string
	PRs    []int
	First  time.Time
	Last   time.Time
	Merged int
	Closed int
	// Reviewing marks a review tree: its pull requests were someone else's, so
	// nothing it touched is work this tree shipped.
	Reviewing bool
	// Reviewed counts pull requests that landed while under review here. It is
	// deliberately not added to Merged — "what shipped" and "what I reviewed"
	// are different questions and summing them answers neither.
	Reviewed int
}

// History reconstructs what has shipped from the event ledger.
//
// The status half of "memory" is structured and exact, so it must not become a
// search problem: git, the ledger and the PR cache already hold every fact, and
// they need an interface rather than an index. The PR cache deliberately
// retains entries for removed supatrees, so history survives a reap for free.
func History(since time.Time, repo string) ([]HistoryEntry, error) {
	evs, err := ReadEvents(since)
	if err != nil {
		return nil, err
	}
	byTree := map[string]*HistoryEntry{}
	for _, ev := range evs {
		if repo != "" && ev.Member != repo {
			continue
		}
		e, ok := byTree[ev.Tree]
		if !ok {
			e = &HistoryEntry{Tree: ev.Tree, First: ev.At}
			byTree[ev.Tree] = e
		}
		e.Last = ev.At
		if ev.Member != "" && !contains(e.Repos, ev.Member) {
			e.Repos = append(e.Repos, ev.Member)
		}
		if ev.PR > 0 && !containsInt(e.PRs, ev.PR) {
			e.PRs = append(e.PRs, ev.PR)
		}
		// A merge on a review tree is the *author's*; counting it here would
		// credit this tree with shipping someone else's change. Reviews are
		// counted on their own axis instead, which is also the honest answer to
		// "what did I review this month".
		switch {
		case ev.Mode == ModeReviewing:
			e.Reviewing = true
			if ev.Kind == EventMerged || ev.Kind == EventClosed {
				e.Reviewed++
			}
		case ev.Kind == EventMerged:
			e.Merged++
		case ev.Kind == EventClosed:
			e.Closed++
		}
	}
	out := make([]HistoryEntry, 0, len(byTree))
	for _, e := range byTree {
		sort.Strings(e.Repos)
		sort.Ints(e.PRs)
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out, nil
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func containsInt(ns []int, n int) bool {
	for _, v := range ns {
		if v == n {
			return true
		}
	}
	return false
}

// isNote reports whether a file in notes/ is a memory rather than the
// scaffolded README. The README explains what to write here and cites examples,
// so searching it means every query matches its own instructions — found while
// testing, and exactly the kind of false positive that makes a store look
// useless on first use.
func isNote(name string) bool {
	return strings.HasSuffix(name, ".md") && name != "README.md"
}

// RecallLogPath records what the memory store was asked for and whether it
// answered.
//
// Every memory system dies the same way: things get written, nothing gets read,
// and nobody notices for months. Logging hits and misses from the first day is
// what makes that visible — and gives the kill criterion something to measure.
// A store nothing has read in 30 days should be deleted, not debugged.
func RecallLogPath() string {
	return filepath.Join(CacheDir(), "recall.jsonl")
}

// RecallHit is one query against the curated notes.
type RecallHit struct {
	At    time.Time `json:"at"`
	Query string    `json:"query"`
	Stack string    `json:"stack,omitempty"`
	Hits  int       `json:"hits"`
}

// Recall searches a stack's notes for a query, returning matching files with
// the lines that matched.
//
// Substring search over a directory of markdown, deliberately. At the volume
// this system produces — tens to low hundreds of finished supatrees a year —
// grep beats embedding retrieval on precision and on being debuggable, and the
// query log above is what would eventually justify something cleverer with a
// real corpus rather than on spec.
func Recall(stackPath, query string) (string, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return "", fmt.Errorf("a query is required")
	}
	dir := NotesDir(stackPath)
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		// A stack with no notes yet is the ordinary case, and it is still a
		// recorded miss: "nobody has written anything here" is exactly what the
		// query log exists to make visible.
		logRecall(RecallHit{Query: query, Stack: stackPath})
		return "no notes for this stack yet", nil //nolint:nilerr // an empty store is an answer, not a failure
	}

	var b strings.Builder
	files := 0
	for _, e := range entries {
		if !isNote(e.Name()) || e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var matched []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(strings.ToLower(line), q) {
				matched = append(matched, strings.TrimSpace(line))
			}
		}
		if len(matched) == 0 {
			continue
		}
		files++
		fmt.Fprintf(&b, "%s/%s:\n", NotesDirName, e.Name())
		for _, line := range matched {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	logRecall(RecallHit{Query: query, Stack: stackPath, Hits: files})
	if files == 0 {
		return fmt.Sprintf("nothing in notes matches %q", query), nil
	}
	return b.String(), nil
}

func logRecall(h RecallHit) {
	h.At = time.Now().UTC()
	_ = config.WithFileLock(RecallLogPath()+".lock", func() error {
		return appendJSONL(RecallLogPath(), []RecallHit{h})
	})
}

// MemorySummary is the short memory section injected into info.md.
//
// Storage was never the hard problem; deciding what loads at session start
// without drowning the context is. So this is hard-capped: everything else sits
// behind `recall`, which an agent calls when it actually wants more. Write-time
// is cheap, read-time is the budget.
func MemorySummary(stackPath string, max int) string {
	entries, err := os.ReadDir(NotesDir(stackPath))
	if err != nil {
		return ""
	}
	type note struct {
		name string
		mod  time.Time
	}
	var notes []note
	for _, e := range entries {
		if !isNote(e.Name()) || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		notes = append(notes, note{e.Name(), info.ModTime()})
	}
	if len(notes) == 0 {
		return ""
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].mod.After(notes[j].mod) })
	if len(notes) > max {
		notes = notes[:max]
	}
	var b strings.Builder
	for _, n := range notes {
		fmt.Fprintf(&b, "- `%s/%s`\n", NotesDirName, n.name)
	}
	return b.String()
}

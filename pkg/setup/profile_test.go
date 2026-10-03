package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

func TestProfileJSON(t *testing.T) {
	p := Profile{
		Name:     "supatree-agent",
		Extends:  []string{"claude-code"},
		Allow:    []string{"/a"},
		ReadFile: []string{"/r"},
		Deny:     []string{"/a/x/**"},
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(p.JSON()), &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	fs := doc["filesystem"].(map[string]any)
	if got := fs["allow"].([]any); len(got) != 1 || got[0] != "/a" {
		t.Errorf("allow = %v", got)
	}
	if got := fs["deny"].([]any); len(got) != 1 {
		t.Errorf("deny = %v", got)
	}
	// Empty sections are omitted, not written as null: nono rejects null lists.
	if _, ok := fs["allow_file"]; ok {
		t.Error("empty allow_file was written")
	}
	if doc["meta"].(map[string]any)["name"] != "supatree-agent" {
		t.Errorf("meta = %v", doc["meta"])
	}
}

func TestProfileWrite(t *testing.T) {
	testutil.IsolateHome(t)
	path, err := Profile{Name: "x"}.Write()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(NonoProfilesDir(), "x.json"); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error(err)
	}
	if _, err := (Profile{}).Write(); err == nil {
		t.Error("wrote a profile with no name")
	}
}

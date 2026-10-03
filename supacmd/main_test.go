package supacmd

import (
	"os"
	"testing"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// TestMain points HOME and the XDG dirs at a throwaway directory for the whole
// binary, so a test that forgets to isolate itself still cannot write into the
// real ~/.config, ~/.local/state or ~/.cache.
func TestMain(m *testing.M) {
	cleanup := testutil.IsolateProcess()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

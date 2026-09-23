package supacmd

import (
	"os"
	"strings"
	"testing"
)

// A repeating error must not grow watch.log without bound: it rotates to one
// previous generation, and the line that tripped the rotation is not lost.
func TestWatchLogRotates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	openWatchLog()
	defer closeWatchLog()
	if watchLog.f == nil {
		t.Fatal("watch log did not open")
	}

	line := strings.Repeat("x", 1000)
	for i := 0; i < (watchLogMax/1000)+50; i++ {
		logWatch("%s", line)
	}
	logWatch("last line")

	cur, err := os.ReadFile(watchLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) > watchLogMax {
		t.Errorf("watch.log is %d bytes, over the %d cap", len(cur), watchLogMax)
	}
	if !strings.Contains(string(cur), "last line") {
		t.Error("the newest line is not in the current log")
	}
	if st, err := os.Stat(watchLogPath() + ".1"); err != nil || st.Size() == 0 {
		t.Errorf("no previous generation kept (%v)", err)
	}
}

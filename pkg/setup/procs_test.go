package setup

import "testing"

func TestParseProcs(t *testing.T) {
	out := `  1 /sbin/launchd
 42 /Users/x/go/bin/supatree mcp
 43 supatree ls
 44 /usr/bin/supatree-other
 45 bash -c while true; do supatree ls; done
 99 /Users/x/go/bin/supatree migrate
`
	got := parseProcs(out, "supatree", 99)
	if len(got) != 2 || got[0].PID != 42 || got[1].PID != 43 {
		t.Errorf("parseProcs = %+v, want pids 42 and 43", got)
	}
}

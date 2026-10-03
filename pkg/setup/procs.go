package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Proc is a running process.
type Proc struct {
	PID  int
	Args string
}

func (p Proc) String() string { return fmt.Sprintf("%d %s", p.PID, p.Args) }

// OtherProcesses lists running processes whose executable is named binary,
// other than this one. A migration uses it to refuse while anything could still
// be writing the layout it is about to move: a pane-less agent's MCP server, a
// sidebar's restart loop, an orphaned PM.
func OtherProcesses(binary string) ([]Proc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	return parseProcs(string(out), binary, os.Getpid()), nil
}

func parseProcs(out, binary string, self int) []Proc {
	var procs []Proc
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == self {
			continue
		}
		if filepath.Base(fields[1]) != binary {
			continue
		}
		procs = append(procs, Proc{PID: pid, Args: strings.Join(fields[1:], " ")})
	}
	return procs
}

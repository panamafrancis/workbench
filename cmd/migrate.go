package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/setup"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

var (
	migrateDryRun    bool
	migrateSkipCheck bool
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Move an older ~/.workbench layout to the XDG layout",
	Long: `migrate moves workbench's files out of ~/.workbench: config to
~/.config/workbench, state and logs to ~/.local/state/workbench, the PR cache
to ~/.cache/workbench. The removed per-repo hook fields (startup_script,
cleanup_script, startup_instructions) are dropped. New worktrees go under
~/workbench unless worktree_base says otherwise. ~/.workbench is renamed to
~/.workbench.pre-xdg, not deleted.

It refuses while any worktree is recorded, a wb-* zellij session is open, or
any other workbench process is alive. --dry-run shows the plan.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return migrateWorkbench()
	},
}

func migrateWorkbench() error {
	if !config.OldLayout() {
		if _, err := os.Stat(config.LegacyDir()); err == nil {
			return fmt.Errorf("already migrated: %s exists; %s is a leftover", config.ConfigPath(), config.LegacyDir())
		}
		return fmt.Errorf("nothing to migrate: there is no %s", config.LegacyDir())
	}
	old := config.LegacyDir()
	oldCfg, err := config.LoadFile(config.LegacyConfigPath())
	if err != nil {
		return err
	}

	var problems []string
	for _, r := range oldCfg.Repos {
		for _, w := range r.Worktrees {
			problems = append(problems, fmt.Sprintf("worktree %s (%s) still exists — remove it: workbench rm worktree %s", w.Name, r.Alias, w.Name))
		}
	}
	if !migrateSkipCheck {
		if sessions, err := zellij.ListSessions(); err == nil {
			for _, s := range sessions {
				if !s.Exited && strings.HasPrefix(s.Name, wbZ.SessionPrefix) {
					problems = append(problems, "zellij session "+s.Name+" is running — quit it")
				}
			}
		}
		if procs, err := setup.OtherProcesses("workbench"); err == nil {
			for _, p := range procs {
				problems = append(problems, "process still running: "+p.String())
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("not migrating while workbench is in use:\n  - %s", strings.Join(problems, "\n  - "))
	}

	fmt.Printf("moving %s to:\n  config  %s\n  state   %s\n  cache   %s\n", old, config.ConfigDir(), config.StateDir(), config.CacheDir())
	if migrateDryRun {
		fmt.Println("\n(dry run — nothing changed)")
		return nil
	}

	moves := []struct{ from, to string }{
		{"state.yml", config.StatePath()},
		{"logs", config.LogsDir()},
		{"cache/pr-status.json", config.PRCachePath()},
	}
	for _, m := range moves {
		if err := config.CopyPath(filepath.Join(old, m.from), m.to); err != nil {
			return fmt.Errorf("copy %s: %w", m.from, err)
		}
	}
	// The default worktree base moved with everything else; a config that
	// named the old default explicitly is pointed at the new one.
	if filepath.Clean(oldCfg.WorktreeBase) == filepath.Join(old, "worktrees") {
		oldCfg.WorktreeBase = ""
	}
	// Last but one: the new config's existence ends the old layout, so
	// everything before it can be re-run after a failure.
	if err := oldCfg.Save(); err != nil {
		return err
	}
	cfg = oldCfg
	// The generated profile granted ~/.workbench, which is about to be gone.
	if _, err := os.Stat(setup.NonoProfilePath(localProfileName)); err == nil {
		initNonInteractive = true
		if err := writeLocalProfile(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not regenerate the nono profile (%v) — run: workbench init --profile\n", err)
		}
	}
	backup := old + ".pre-xdg"
	if _, err := os.Stat(backup); err == nil {
		backup += "." + time.Now().Format("20060102-150405")
	}
	if err := os.Rename(old, backup); err != nil {
		return errors.Join(fmt.Errorf("migrated, but could not set %s aside", old), err)
	}
	fmt.Printf("\nmigrated. The old layout is at %s — delete it by hand once you are happy.\n", backup)
	return nil
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "show the plan, change nothing")
	migrateCmd.Flags().BoolVar(&migrateSkipCheck, "skip-live-check", false, "skip the running-process and session checks (tests only)")
	_ = migrateCmd.Flags().MarkHidden("skip-live-check")
	rootCmd.AddCommand(migrateCmd)
}

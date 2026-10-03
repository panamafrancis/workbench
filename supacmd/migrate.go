package supacmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var (
	migrateDryRun    bool
	migrateSkipCheck bool
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Move an older ~/.supatree layout to the XDG layout, with supatree owning its clones",
	Long: `migrate moves supatree's files out of ~/.supatree: config to
~/.config/supatree, state to ~/.local/state/supatree, caches to
~/.cache/supatree, stacks to ~/supatree/stacks. It clones every stack member
into supatree's own repo cache (~/supatree/repos) and rewrites each stack's
supatree.yml to name members by git URL, as a commit — resolving a personal
ssh alias to its real host, and adding the git insteadOf line that keeps the
alias's key in use. Models and copy_files are imported, read-only, from
workbench's config. ~/.supatree is renamed to ~/.supatree.pre-xdg, not deleted.

It refuses while any supatree exists, the watcher runs, an st-* zellij session
is open, or any other supatree process is alive: close every tree with
"supatree rm", quit every supatree session, then run it. --dry-run shows the
plan without changing anything.`,
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		return supatree.Migrate(supatree.MigrateOptions{
			DryRun:        migrateDryRun,
			SkipLiveCheck: migrateSkipCheck,
			Out:           os.Stdout,
		})
	},
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "show the plan, change nothing")
	migrateCmd.Flags().BoolVar(&migrateSkipCheck, "skip-live-check", false, "skip the running-process and session checks (tests only)")
	_ = migrateCmd.Flags().MarkHidden("skip-live-check")
	rootCmd.AddCommand(migrateCmd)
}

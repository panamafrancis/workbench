package supacmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
	stui "github.com/panamafrancis/workbench/pkg/supatree/tui"
)

var (
	scaffoldPath  string
	scaffoldRepos string
)

var scaffoldCmd = &cobra.Command{
	Use:   "scaffold <name>",
	Short: "Create a reusable stack (repo set) for future supatrees",
	Long: `Create a "stack": the reusable definition of which repos a cross-repo
issue spans and how they depend on each other. Later, "supatree new --stack
<name>" spins up a fresh set of worktrees (one per repo) from it.

A stack is itself a small git repo — it holds supatree.yml (the repo list +
dependency edges), AGENTS.md (instructions for agents working in it), and a
scripts/ directory. By default it is created at ~/.supatree/stacks/<name>/;
pass --path to put it somewhere you'll push to a remote and share.

Member repos are referenced by their workbench alias, so register them first
with "workbench add repo <path> --alias=<alias>". Omit --repos to pick them
interactively.

Examples:
  # interactively pick repos, stack stored at ~/.supatree/stacks/fraud
  supatree scaffold fraud

  # non-interactive (scripts/CI)
  supatree scaffold fraud --repos=terraform,keystone-api,admin-frontend

  # keep the stack repo in a shareable location
  supatree scaffold fraud --repos=terraform,keystone --path=~/stacks/fraud

After scaffolding, edit supatree.yml to add dependency edges, then:
  supatree new --stack=fraud`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		members, err := selectRepos()
		if err != nil {
			return err
		}

		res, err := supatree.Scaffold(name, scaffoldPath, members, wbCfg)
		if err != nil {
			return err
		}
		fmt.Printf("Scaffolded stack %q at %s\n", res.Alias, res.Path)
		fmt.Printf("  members: %s\n", strings.Join(members, ", "))
		fmt.Printf("  add dependency edges in %s/%s, then: supatree new --stack %s\n", res.Path, supatree.SpecName, res.Alias)
		return nil
	},
}

// selectRepos resolves the member repos: from --repos when given, otherwise via
// an interactive picker on a TTY, otherwise an error.
func selectRepos() ([]string, error) {
	if scaffoldRepos != "" {
		var members []string
		for _, r := range strings.Split(scaffoldRepos, ",") {
			if r = strings.TrimSpace(r); r != "" {
				members = append(members, r)
			}
		}
		return members, nil
	}

	aliases := repoAliases()
	if len(aliases) == 0 {
		return nil, fmt.Errorf("no repos registered with workbench — run: workbench add repo <path> --alias=<alias>")
	}
	if !isInteractive() {
		return nil, fmt.Errorf("no repos given — pass --repos=<a,b,c> (available: %s)", strings.Join(aliases, ", "))
	}
	members, err := stui.PickRepos(aliases)
	if err != nil {
		if errors.Is(err, stui.ErrPickerCancelled) {
			fmt.Fprintln(os.Stderr, "cancelled")
			os.Exit(1)
		}
		return nil, err
	}
	return members, nil
}

func repoAliases() []string {
	aliases := make([]string, 0, len(wbCfg.Repos))
	for _, r := range wbCfg.Repos {
		aliases = append(aliases, r.Alias)
	}
	return aliases
}

func init() {
	scaffoldCmd.Flags().StringVar(&scaffoldRepos, "repos", "", "comma-separated workbench repo aliases (omit to pick interactively)")
	scaffoldCmd.Flags().StringVar(&scaffoldPath, "path", "", "location for the stack repo (default: ~/.supatree/stacks/<name>)")
}

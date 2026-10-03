package supacmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var (
	stackAddAs   string
	stackCloneAs string
)

var stackCmd = &cobra.Command{
	Use:   "stack",
	Short: "Create and edit stacks — the repo sets supatrees are made from",
}

var stackNewCmd = &cobra.Command{
	Use:   "new <name>",
	Short: "Create a stack",
	Long:  stackNewLong,
	Args:  cobra.ExactArgs(1),
	RunE:  runStackNew,
}

var stackAddCmd = &cobra.Command{
	Use:   "add <stack> <owner/repo | url>",
	Short: "Add a member repo to a stack (committed to the stack repo)",
	Long: "add records a member in the stack's supatree.yml, as a commit, and clones it\n" +
		"into the repo cache now so the first tree does not wait for it. The alias —\n" +
		"repos/<alias>/ in every tree — defaults to the repository's name; --as picks\n" +
		"another. Existing trees get it on their next sync.",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		arg := args[1]
		if stackAddAs != "" {
			arg = stackAddAs + "=" + arg
		}
		alias, url, err := supatree.ParseMemberArg(arg)
		if err != nil {
			return err
		}
		if err := stCfg.StackAdd(args[0], alias, url, true); err != nil {
			return err
		}
		fmt.Printf("added %s (%s) to %s — trees get it on: supatree sync <tree>\n", alias, url, args[0])
		return nil
	},
}

var stackRmCmd = &cobra.Command{
	Use:   "rm <stack> <alias>",
	Short: "Remove a member from a stack (committed to the stack repo)",
	Long: "rm drops a member, and every dependency edge naming it, from the stack's\n" +
		"supatree.yml as a commit. Trees already holding it keep it until\n" +
		"`supatree sync <tree> --prune`. The clone stays in the repo cache.",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := stCfg.StackRm(args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("removed %s from %s\n", args[1], args[0])
		return nil
	},
}

var stackDepCmd = &cobra.Command{
	Use:   "dep <stack> <from> <to>",
	Short: "Record that <from> depends on <to> (to's pull request merges first)",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := stCfg.StackDep(args[0], args[1], args[2]); err != nil {
			return err
		}
		fmt.Printf("%s now depends on %s\n", args[1], args[2])
		return nil
	},
}

var stackCloneCmd = &cobra.Command{
	Use:   "clone <git-url>",
	Short: "Adopt a teammate's stack: clone it, register it, fill the repo cache",
	Long: "clone fetches a shared stack repo into ~/supatree/stacks/<name> (or --path),\n" +
		"registers it, and clones each member into the repo cache. If the stack has a\n" +
		"scripts/setup it is printed: it runs for every tree you make from this stack,\n" +
		"so read it.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := stCfg.StackClone(args[0], stackCloneAs, stackPath)
		if err != nil {
			return err
		}
		fmt.Printf("registered stack %q at %s\n", res.Alias, res.Path)
		for _, w := range res.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		if res.Setup != "" {
			fmt.Printf("\nThis stack's %s runs once for every tree made from it:\n\n", supatree.SetupScript)
			for _, line := range strings.Split(strings.TrimRight(res.Setup, "\n"), "\n") {
				fmt.Printf("  │ %s\n", line)
			}
			fmt.Println()
		}
		fmt.Println("Personal files a repo needs (a .env) go in its cache clone, listed under")
		fmt.Printf("`repos: <host/owner/repo>: copy_files:` in %s.\n", supatree.ConfigPath())
		return nil
	},
}

func init() {
	addStackNewFlags(stackNewCmd)
	stackAddCmd.Flags().StringVar(&stackAddAs, "as", "", "alias for the member (default: the repository name)")
	stackCloneCmd.Flags().StringVar(&stackCloneAs, "as", "", "name to register the stack under (default: the repository name)")
	stackCloneCmd.Flags().StringVar(&stackPath, "path", "", "where to clone the stack repo (default: ~/supatree/stacks/<name>)")
	stackCmd.AddCommand(stackNewCmd, stackAddCmd, stackRmCmd, stackDepCmd, stackCloneCmd)
	rootCmd.AddCommand(stackCmd)
}

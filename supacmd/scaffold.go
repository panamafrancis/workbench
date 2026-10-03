package supacmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
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

A stack is itself a small git repo — it holds supatree.yml (each member's
alias and git URL, plus dependency edges), AGENTS.md (instructions for agents
working in it), and a scripts/ directory. By default it is created at
~/supatree/stacks/<name>/; pass --path to put it somewhere you'll push to a
remote and share. Everything in it is shareable: members are named by URL,
never by a local path.

Members are given as owner/repo (a GitHub repository over ssh), as a git URL,
or as alias=<either>. Each is cloned into supatree's own repo cache
(~/supatree/repos/) if it is not there yet. Omit --repos to pick from the
repositories already in the cache.

Examples:
  supatree scaffold fraud --repos=fraud-zero/terraform,fraud-zero/keystone-api
  supatree scaffold fraud --repos=api=git@github.com:fraud-zero/keystone-api.git
  supatree scaffold fraud --repos=fraud-zero/terraform --path=~/stacks/fraud

After scaffolding, edit supatree.yml to add dependency edges, then:
  supatree new --stack=fraud`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		members, err := selectRepos()
		if err != nil {
			return err
		}

		res, err := supatree.Scaffold(stCfg, name, scaffoldPath, members)
		if err != nil {
			return err
		}
		fmt.Printf("Scaffolded stack %q at %s\n", res.Alias, res.Path)
		for _, alias := range sortedKeys(members) {
			fmt.Printf("  %-20s %s\n", alias, members[alias])
		}
		fmt.Printf("  add dependency edges in %s/%s, then: supatree new --stack %s\n", res.Path, supatree.SpecName, res.Alias)
		return nil
	},
}

// selectRepos resolves the members, alias → URL: from --repos when given,
// otherwise via an interactive picker over the repo cache, otherwise an error.
func selectRepos() (map[string]string, error) {
	if scaffoldRepos != "" {
		return parseMemberArgs(strings.Split(scaffoldRepos, ","))
	}

	cached, err := supatree.ListCachedRepos()
	if err != nil {
		return nil, err
	}
	if len(cached) == 0 {
		return nil, fmt.Errorf("no repos given — pass --repos=<owner/repo,...> (the repo cache is empty, so there is nothing to pick from)")
	}
	byKey := make(map[string]string, len(cached))
	keys := make([]string, 0, len(cached))
	for _, r := range cached {
		byKey[r.Key] = r.URL
		keys = append(keys, r.Key)
	}
	if !isInteractive() {
		return nil, fmt.Errorf("no repos given — pass --repos=<owner/repo,...> (cached: %s)", strings.Join(keys, ", "))
	}
	picked, err := stui.PickRepos(keys)
	if err != nil {
		if errors.Is(err, stui.ErrPickerCancelled) {
			fmt.Fprintln(os.Stderr, "cancelled")
			os.Exit(1)
		}
		return nil, err
	}
	args := make([]string, 0, len(picked))
	for _, k := range picked {
		args = append(args, byKey[k])
	}
	return parseMemberArgs(args)
}

// parseMemberArgs reads member arguments into alias → URL, refusing two
// members that would share an alias.
func parseMemberArgs(args []string) (map[string]string, error) {
	members := map[string]string{}
	for _, a := range args {
		if strings.TrimSpace(a) == "" {
			continue
		}
		alias, url, err := supatree.ParseMemberArg(a)
		if err != nil {
			return nil, err
		}
		if prev, dup := members[alias]; dup {
			return nil, fmt.Errorf("%s and %s would both be %q — name one with alias=<url>", prev, url, alias)
		}
		members[alias] = url
	}
	return members, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func init() {
	scaffoldCmd.Flags().StringVar(&scaffoldRepos, "repos", "", "comma-separated members: owner/repo, a git URL, or alias=<either> (omit to pick from the repo cache)")
	scaffoldCmd.Flags().StringVar(&scaffoldPath, "path", "", "location for the stack repo (default: ~/supatree/stacks/<name>)")
}

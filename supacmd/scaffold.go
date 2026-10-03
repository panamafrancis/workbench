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
	stackPath    string
	stackRepos   string
	stackFrom    string
	stackFromOrg string
)

const stackNewLong = `Create a "stack": the reusable definition of which repos a cross-repo
issue spans and how they depend on each other. Later, "supatree new --stack
<name>" spins up a fresh set of worktrees (one per repo) from it.

A stack is itself a small git repo — it holds supatree.yml (each member's
alias and git URL, plus dependency edges), AGENTS.md (instructions for agents
working in it), and a scripts/ directory. By default it is created at
~/supatree/stacks/<name>/; pass --path to put it anywhere, for instance
somewhere you push to a remote and share. Everything in it is shareable:
members are named by URL, never by a local path.

Members come from one of:
  --repos      owner/repo (GitHub, over ssh), a git URL, or alias=<either>
  --from DIR   the origin of each git repo directly under DIR — only the URL
               is read; supatree clones its own copy
  --from-org   an organization's repositories, via gh (archived repos and
               forks skipped)
  (nothing)    a picker over the repositories already in supatree's cache

--from and --from-org open a picker; each chosen repository is cloned into
supatree's repo cache (~/supatree/repos/) if it is not there yet.

Examples:
  supatree stack new fraud --repos=fraud-zero/terraform,fraud-zero/keystone-api
  supatree stack new fraud --from ~/code/fraud-zero
  supatree stack new fraud --from-org fraud-zero --path ~/stacks/fraud

Then: supatree stack dep fraud keystone-api terraform; supatree new --stack fraud`

func runStackNew(cmd *cobra.Command, args []string) error {
	name := args[0]
	members, err := selectRepos()
	if err != nil {
		return err
	}
	res, err := supatree.Scaffold(stCfg, name, stackPath, members)
	if err != nil {
		return err
	}
	fmt.Printf("Created stack %q at %s\n", res.Alias, res.Path)
	for _, alias := range sortedKeys(members) {
		fmt.Printf("  %-20s %s\n", alias, members[alias])
	}
	fmt.Printf("Add dependency edges with: supatree stack dep %s <from> <to>\nThen: supatree new --stack %s\n", res.Alias, res.Alias)
	return nil
}

// scaffoldCmd is the original name of `stack new`, kept as an alias.
var scaffoldCmd = &cobra.Command{
	Use:   "scaffold <name>",
	Short: "Create a stack (alias of: supatree stack new)",
	Long:  stackNewLong,
	Args:  cobra.ExactArgs(1),
	RunE:  runStackNew,
}

// selectRepos resolves the members, alias → URL, from whichever source was
// given.
func selectRepos() (map[string]string, error) {
	if stackRepos != "" {
		return parseMemberArgs(strings.Split(stackRepos, ","))
	}
	var candidates map[string]string
	var source string
	switch {
	case stackFrom != "":
		dir := expandHome(stackFrom)
		found, err := supatree.ScanOrigins(dir)
		if err != nil {
			return nil, err
		}
		candidates, source = found, "git repos with an origin under "+dir
	case stackFromOrg != "":
		found, err := supatree.OrgRepos(stackFromOrg)
		if err != nil {
			return nil, err
		}
		candidates, source = found, stackFromOrg+"'s repositories"
	default:
		cached, err := supatree.ListCachedRepos()
		if err != nil {
			return nil, err
		}
		candidates = map[string]string{}
		for _, r := range cached {
			candidates[r.Key] = r.URL
		}
		source = "the repo cache"
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("nothing to pick from in %s — pass --repos=<owner/repo,...>, --from <dir> or --from-org <org>", source)
	}
	names := sortedKeys(candidates)
	if !isInteractive() {
		return nil, fmt.Errorf("pick members with --repos (from %s: %s)", source, strings.Join(names, ", "))
	}
	picked, err := stui.PickRepos(names)
	if err != nil {
		if errors.Is(err, stui.ErrPickerCancelled) {
			fmt.Fprintln(os.Stderr, "cancelled")
			os.Exit(1)
		}
		return nil, err
	}
	args := make([]string, 0, len(picked))
	for _, k := range picked {
		args = append(args, candidates[k])
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

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return home + "/" + rest
		}
	}
	return p
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func addStackNewFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&stackRepos, "repos", "", "comma-separated members: owner/repo, a git URL, or alias=<either>")
	cmd.Flags().StringVar(&stackFrom, "from", "", "pick from the origins of the git repos directly under this directory")
	cmd.Flags().StringVar(&stackFromOrg, "from-org", "", "pick from a GitHub organization's repositories (via gh)")
	cmd.Flags().StringVar(&stackPath, "path", "", "location for the stack repo (default: ~/supatree/stacks/<name>)")
	cmd.MarkFlagsMutuallyExclusive("repos", "from", "from-org")
}

func init() {
	addStackNewFlags(scaffoldCmd)
}

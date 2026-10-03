package supacmd

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "The repo cache: supatree's own clones, one per remote",
}

var repoLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List the clones in the repo cache, their size, and the trees using each",
	RunE: func(cmd *cobra.Command, args []string) error {
		repos, err := supatree.ListCachedRepos()
		if err != nil {
			return err
		}
		if len(repos) == 0 {
			fmt.Printf("the repo cache (%s) is empty\n", supatree.ReposDir())
			return nil
		}
		insts, _ := supatree.List(stCfg)
		usage := supatree.CacheUsage(insts)
		for _, r := range repos {
			var trees []string
			for _, u := range supatree.UsageOf(usage, r.Path) {
				trees = append(trees, u.Tree+":"+u.Alias)
			}
			used := "unused"
			if len(trees) > 0 {
				used = strings.Join(trees, ", ")
			}
			fmt.Printf("%-50s %8s  %s\n", r.Key, humanSize(dirSize(r.Path)), used)
		}
		return nil
	},
}

var repoRmCmd = &cobra.Command{
	Use:   "rm <owner/repo | key | url>",
	Short: "Delete a clone from the repo cache (refused while a tree uses it)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := supatree.FindCachedRepo(args[0])
		if err != nil {
			return err
		}
		insts, err := supatree.List(stCfg)
		if err != nil {
			return err
		}
		if err := supatree.RemoveCachedRepo(r, insts); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", r.Key)
		return nil
	},
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best effort: a size is an estimate
		}
		if info, err := d.Info(); err == nil && !d.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}

func init() {
	repoCmd.AddCommand(repoLsCmd, repoRmCmd)
	rootCmd.AddCommand(repoCmd)
}

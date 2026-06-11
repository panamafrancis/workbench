package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/docs"
)

var docsCmd = &cobra.Command{
	Use:   "docs [topic]",
	Short: "Show workbench documentation",
	Long:  "Show workbench documentation. Without a topic, lists available topics.\n\n" + docs.ListTopics(),
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			fmt.Print(docs.ListTopics())
			return nil
		}
		topic := args[0]
		if topic == "all" {
			fmt.Print(docs.All())
			return nil
		}
		content, ok := docs.Get(topic)
		if !ok {
			fmt.Print(docs.ListTopics())
			return fmt.Errorf("unknown topic %q", topic)
		}
		fmt.Print(content)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(docsCmd)
}

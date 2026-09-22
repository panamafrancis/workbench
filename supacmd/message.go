package supacmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/workbench/pkg/supatree"
)

var messageFrom string

var messageCmd = &cobra.Command{
	Use:   "message <supatree> <agent> <text>...",
	Short: "Leave a message in an agent's mailbox",
	Long: "message delivers text to an agent's mailbox inside a supatree. The agent\n" +
		"reads it on its next turn via the `inbox` MCP tool.\n\n" +
		"Delivery does not require the agent to be running, or even to exist yet as a\n" +
		"process — the mailbox is a file. That is what makes it the contract for\n" +
		"agent-to-agent messaging: a message bus, where the model's CLI has one, only\n" +
		"makes the same message arrive sooner.",
	Args: cobra.MinimumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		inst, err := supatree.Get(stCfg, args[0])
		if err != nil {
			return err
		}
		agent := args[1]
		text := strings.Join(args[2:], " ")

		// Deliberately not gated on the agent existing in agents.yml: a message
		// left for an agent that has not been launched yet is waiting for it
		// when it starts, which is more useful than a refusal.
		if err := supatree.Deliver(inst.Root, agent, messageFrom, text); err != nil {
			return err
		}
		fmt.Printf("delivered to %s/%s (%d unread)\n", inst.Name, agent, supatree.MailboxCount(inst.Root, agent))
		return nil
	},
}

var inboxCmd = &cobra.Command{
	Use:   "inbox <supatree> <agent>",
	Short: "Show an agent's pending messages",
	Long: "inbox prints what is waiting in an agent's mailbox without consuming it.\n" +
		"Only the agent itself clears its mail, via the `inbox` MCP tool — so reading\n" +
		"here never costs it a message it has not acted on.",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		inst, err := supatree.Get(stCfg, args[0])
		if err != nil {
			return err
		}
		msgs, err := supatree.Mail(inst.Root, args[1])
		if err != nil {
			return err
		}
		fmt.Println(supatree.FormatMail(msgs))
		return nil
	},
}

func init() {
	messageCmd.Flags().StringVar(&messageFrom, "from", "cli", "who the message is from")
}

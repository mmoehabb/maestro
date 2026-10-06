package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
)

func newLifecycleCmd(action string) *cobra.Command {
	descriptions := map[string]string{
		"archive": "Hide a task tab, preserving its worktree and history",
		"reopen":  "Restore an archived task tab",
		"rm":      "Delete an archived task and its saved history; retain Git files",
	}
	cmd := &cobra.Command{
		Use: action + " <task>", Short: descriptions[action], Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := app.Open(cmd.Context(), ".", config.DefaultPaths())
			if err != nil {
				return err
			}
			defer s.Store.Close()
			switch action {
			case "archive":
				err = s.Archive(cmd.Context(), args[0])
			case "reopen":
				err = s.Reopen(cmd.Context(), args[0])
			case "rm":
				err = s.DeleteArchived(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s complete (worktree and branch retained)\n", args[0], action)
			return nil
		},
	}
	if action == "rm" {
		cmd.Aliases = []string{"delete"}
	}
	return cmd
}

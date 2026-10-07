package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
)

func newLifecycleCmd(action string) *cobra.Command {
	var cleanup, force bool
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
				if force && !cleanup {
					return fmt.Errorf("--force requires --cleanup")
				}
				if cleanup {
					_, err = s.Workflow(cmd.Context(), args[0], "cleanup", core.WorkflowOptions{Force: force})
				} else {
					err = s.Archive(cmd.Context(), args[0])
				}
			case "reopen":
				err = s.Reopen(cmd.Context(), args[0])
			case "rm":
				err = s.DeleteArchived(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s complete\n", args[0], action)
			return nil
		},
	}
	if action == "archive" {
		cmd.Flags().BoolVar(&cleanup, "cleanup", false, "Remove the worktree and branch after safety checks; retain history")
		cmd.Flags().BoolVar(&force, "force", false, "Explicitly discard uncommitted files and allow cleanup without proof of push (requires --cleanup)")
	}
	if action == "rm" {
		cmd.Aliases = []string{"delete"}
	}
	return cmd
}

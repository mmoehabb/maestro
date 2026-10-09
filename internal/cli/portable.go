package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
)

func newCheckpointCmd() *cobra.Command {
	var branch string
	cmd := &cobra.Command{Use: "checkpoint <task>", Short: "Stop the agent and write portable context for review, commit and push", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := app.OpenLocal(cmd.Context(), ".", config.DefaultPaths())
		if err != nil {
			return err
		}
		defer s.Store.Close()
		path, err := s.CheckpointOnBranch(cmd.Context(), args[0], branch)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Checkpoint saved to %s\nReview and commit .maestro/.gitignore and .maestro/tasks with your code, then push the task branch.\n", path)
		return nil
	}}
	cmd.Flags().StringVar(&branch, "branch", "", "Adopt a branch renamed in this task's existing checkout")
	return cmd
}

func newRestoreCmd() *cobra.Command {
	var replace bool
	cmd := &cobra.Command{Use: "restore <branch-or-remote-ref>", Short: "Import a committed task checkpoint from a fetched branch", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := app.OpenLocal(cmd.Context(), ".", config.DefaultPaths())
		if err != nil {
			return err
		}
		defer s.Store.Close()
		if err = s.RestoreTasks(cmd.Context(), args[0], replace); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Task context restored. Open Maestro to continue; existing checkouts may need a Git update first.")
		return nil
	}}
	cmd.Flags().BoolVar(&replace, "replace", false, "Replace conflicting local task metadata/context; preserve Git files and local history")
	return cmd
}

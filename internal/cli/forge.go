package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
)

func newForgeCmd(action string) *cobra.Command {
	var opts core.WorkflowOptions
	descriptions := map[string]string{"push": "Push a task branch", "pr": "Create or show a task's PR", "merge": "Merge a task's PR using the configured method"}
	cmd := &cobra.Command{Use: action + " <task>", Short: descriptions[action], Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
		defer cancel()
		s, err := app.Open(ctx, ".", config.DefaultPaths())
		if err != nil {
			return err
		}
		defer s.Store.Close()
		if action == "pr" {
			opts.Body, opts.BodySet, err = readPRBody(cmd)
			if err != nil {
				return err
			}
		}
		task, err := s.Workflow(ctx, args[0], action, opts)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", task.Slug, task.Lifecycle)
		if task.PRURL != "" {
			fmt.Fprintln(cmd.OutOrStdout(), task.PRURL)
		}
		if task.Lifecycle == "merged" {
			switch s.Config.Git.Cleanup {
			case "auto":
				_, err = s.Workflow(ctx, args[0], "cleanup", core.WorkflowOptions{})
				if err != nil {
					return fmt.Errorf("PR merged; cleanup deferred: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Worktree cleaned; history retained.")
			case "ask":
				fmt.Fprintf(cmd.OutOrStdout(), "Clean up with: maestro archive %s --cleanup\n", task.Slug)
			}
		}
		return nil
	}}
	if action == "pr" {
		cmd.Flags().StringVar(&opts.Title, "title", "", "PR title (defaults to task title)")
		cmd.Flags().StringVar(&opts.Base, "base", "", "PR base branch (required for a commit-only task base)")
		cmd.Flags().StringVar(&opts.Body, "body", "", "Reviewed PR description")
		cmd.Flags().String("body-file", "", "Read PR description from a file")
		cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	}
	if action == "merge" {
		cmd.Flags().StringVar(&opts.ExpectedHead, "head", "", "Require the PR head to match this reviewed commit")
	}
	return cmd
}

func readPRBody(cmd *cobra.Command) (string, bool, error) {
	if cmd.Flags().Changed("body-file") {
		path, err := cmd.Flags().GetString("body-file")
		if err != nil {
			return "", true, err
		}
		body, err := os.ReadFile(path)
		return string(body), true, err
	}
	body, err := cmd.Flags().GetString("body")
	return body, cmd.Flags().Changed("body"), err
}

// Package cli defines Maestro's command-line interface.
//
// Commands not yet delivered return ErrNotImplemented naming their phase.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/version"
)

// ErrNotImplemented is returned by commands scheduled for a later phase.
var ErrNotImplemented = errors.New("not implemented yet")

func notImplemented() func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		return fmt.Errorf("%q: %w (planned for %s, see docs/PLAN.md)", cmd.CommandPath(), ErrNotImplemented, "P3")
	}
}

// NewRootCmd builds the full command tree. Exposed for tests.
func NewRootCmd(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "maestro",
		Short: "tmux for coding agents",
		Long: "Maestro orchestrates coding agents (codex, agy, opencode, ...) in tabs.\n" +
			"Each tab is a task with its own git worktree that becomes a PR.",
		Version:       version.Get().String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          func(cmd *cobra.Command, _ []string) error { return app.Run(cmd.Context(), ".", "", cmd.OutOrStdout()) },
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Version}}\n")

	root.AddCommand(
		newVersionCmd(),
		newTaskCmd(),
		newListCmd(),
		&cobra.Command{Use: "open <task>", Short: "Open the TUI focused on a task", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return app.Run(cmd.Context(), ".", args[0], cmd.OutOrStdout())
		}},
		newSwitchCmd(),
		newHistoryCmd(),
		newNotesCmd(),
		&cobra.Command{Use: "push <task>", Short: "Push a task's branch", Args: cobra.ExactArgs(1), RunE: notImplemented()},
		&cobra.Command{Use: "pr <task>", Short: "Create or open a task's PR", Args: cobra.ExactArgs(1), RunE: notImplemented()},
		&cobra.Command{Use: "merge <task>", Short: "Merge a task's PR", Args: cobra.ExactArgs(1), RunE: notImplemented()},
		newLifecycleCmd("archive"),
		newLifecycleCmd("reopen"),
		newLifecycleCmd("rm"),
		newDoctorCmd(),
		newConfigCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version.Get().String())
		},
	}
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	if err := NewRootCmd(os.Stdout, os.Stderr).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "maestro:", err)
		return 1
	}
	return 0
}

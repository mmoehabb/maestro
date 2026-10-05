// Package cli defines Maestro's command-line interface.
//
// In P0 only `version` is functional; every other command is registered so the
// CLI surface matches docs/PLAN.md, but returns ErrNotImplemented naming the
// phase that delivers it.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/version"
)

// ErrNotImplemented is returned by commands scheduled for a later phase.
var ErrNotImplemented = errors.New("not implemented yet")

func notImplemented(phase string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		return fmt.Errorf("%q: %w (planned for %s, see docs/PLAN.md)", cmd.CommandPath(), ErrNotImplemented, phase)
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
		RunE:          notImplemented("P1"), // opens the TUI
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Version}}\n")

	root.AddCommand(
		newVersionCmd(),
		&cobra.Command{Use: "new <title>", Short: "Create a new task", Args: cobra.ExactArgs(1), RunE: notImplemented("P1")},
		&cobra.Command{Use: "ls", Aliases: []string{"list"}, Short: "List tasks", Args: cobra.NoArgs, RunE: notImplemented("P1")},
		&cobra.Command{Use: "open <task>", Short: "Open the TUI focused on a task", Args: cobra.ExactArgs(1), RunE: notImplemented("P1")},
		&cobra.Command{Use: "switch <task>", Short: "Switch the agent of a task (with context handoff)", Args: cobra.ExactArgs(1), RunE: notImplemented("P2")},
		&cobra.Command{Use: "history <task>", Short: "Show a task's timeline and transcript", Args: cobra.ExactArgs(1), RunE: notImplemented("P2")},
		&cobra.Command{Use: "push <task>", Short: "Push a task's branch", Args: cobra.ExactArgs(1), RunE: notImplemented("P3")},
		&cobra.Command{Use: "pr <task>", Short: "Create or open a task's PR", Args: cobra.ExactArgs(1), RunE: notImplemented("P3")},
		&cobra.Command{Use: "merge <task>", Short: "Merge a task's PR", Args: cobra.ExactArgs(1), RunE: notImplemented("P3")},
		&cobra.Command{Use: "archive <task>", Short: "Archive a task and clean its worktree", Args: cobra.ExactArgs(1), RunE: notImplemented("P3")},
		&cobra.Command{Use: "reopen <task>", Short: "Reopen an archived task", Args: cobra.ExactArgs(1), RunE: notImplemented("P3")},
		&cobra.Command{Use: "rm <task>", Short: "Delete a task and its history", Args: cobra.ExactArgs(1), RunE: notImplemented("P3")},
		&cobra.Command{Use: "doctor", Short: "Diagnose agents, git, auth and terminal capabilities", Args: cobra.NoArgs, RunE: notImplemented("P1")},
		&cobra.Command{Use: "config", Short: "Show or edit configuration", RunE: notImplemented("P1")},
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

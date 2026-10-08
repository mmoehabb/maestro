// Package cli defines Maestro's command-line interface.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/version"
)

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
		newForgeCmd("push"),
		newForgeCmd("pr"),
		newForgeCmd("merge"),
		newLifecycleCmd("archive"),
		newLifecycleCmd("reopen"),
		newLifecycleCmd("rm"),
		newDoctorCmd(),
		newAuthCmd(),
		newConfigCmd(),
		newThemeCmd(),
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
	if handled, code := git.RunAskpass(os.Args[1:], os.Stdout); handled {
		return code
	}
	if err := NewRootCmd(os.Stdout, os.Stderr).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "maestro:", err)
		return 1
	}
	return 0
}

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/daemon"
	"github.com/mmoehabb/maestro/internal/git"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "daemon", Short: "Manage the project's background agent daemon"}
	paths := config.DefaultPaths()
	dir := "."
	serve := &cobra.Command{Use: "serve", Short: "Run the daemon in the foreground", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return app.ServeDaemon(cmd.Context(), dir, paths) }}
	serve.Flags().StringVar(&dir, "dir", ".", "Repository directory")
	serve.Flags().StringVar(&paths.DataDir, "data-dir", paths.DataDir, "Maestro data directory")
	serve.Flags().StringVar(&paths.ConfigFile, "config-file", paths.ConfigFile, "Global configuration file")
	cmd.AddCommand(serve)
	for _, action := range []string{"status", "stop"} {
		cmd.AddCommand(&cobra.Command{Use: action, Short: map[string]string{"status": "Show daemon status", "stop": "Stop all project agents, save history, and shut down the daemon"}[action], Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := git.Discover(cmd.Context(), ".")
			if err != nil {
				return err
			}
			address, err := daemon.Endpoint(config.DefaultPaths().DataDir, repo.Key())
			if err != nil {
				return err
			}
			if action == "stop" {
				return (daemon.Remote{Address: address}).Call(cmd.Context(), "shutdown", core.Request{}, nil)
			}
			status, err := daemon.Probe(cmd.Context(), address)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "PID: %d\nAttached: %t\nRunning agents: %d\nProtocol: %d\n", status.PID, status.Attached, status.Agents, status.Version)
			return nil
		}})
	}
	return cmd
}

func newRenameCmd() *cobra.Command {
	return &cobra.Command{Use: "rename <task> <title>", Short: "Change a task's display title", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := app.Open(cmd.Context(), ".", config.DefaultPaths())
		if err != nil {
			return err
		}
		defer s.Store.Close()
		return s.Rename(cmd.Context(), args[0], args[1])
	}}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{Use: "stop <task>", Short: "Stop a daemon-owned agent and save its history", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := app.Open(cmd.Context(), ".", config.DefaultPaths())
		if err != nil {
			return err
		}
		defer s.Store.Close()
		if s.Remote == nil {
			return fmt.Errorf("no daemon is running for this project")
		}
		return s.Remote.Call(cmd.Context(), "stop", core.Request{Slug: args[0]}, nil)
	}}
}

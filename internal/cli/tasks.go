package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	xterm "github.com/charmbracelet/x/term"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/agent"
	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/term"
	"github.com/mmoehabb/maestro/internal/tui"
)

func newTaskCmd() *cobra.Command {
	var in core.NewTask
	cmd := &cobra.Command{
		Use: "new <title>", Short: "Create a task and worktree; launch with maestro open", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := app.Open(cmd.Context(), ".", config.DefaultPaths())
			if err != nil {
				return err
			}
			defer s.Store.Close()
			in.Title = args[0]
			t, err := s.Create(cmd.Context(), in)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\nBranch: %s\nWorktree: %s\nAgent: %s\nLaunch: maestro open %s\n", t.Slug, t.Branch, t.Worktree, t.Agent, t.Slug)
			return nil
		},
	}
	cmd.Flags().StringVarP(&in.Agent, "agent", "a", "", "Configured agent (defaults to default_agent)")
	cmd.Flags().StringVarP(&in.Base, "base", "b", "", "Base branch or commit")
	cmd.Flags().StringVarP(&in.Prompt, "prompt", "p", "", "First prompt, saved for agent launch")
	return cmd
}

func newListCmd() *cobra.Command {
	var all, archived, asJSON bool
	cmd := &cobra.Command{
		Use: "ls", Aliases: []string{"list", "tabs"}, Short: "List task tabs", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := app.Open(cmd.Context(), ".", config.DefaultPaths())
			if err != nil {
				return err
			}
			defer s.Store.Close()
			tasks, err := s.List(cmd.Context(), all || archived)
			if err != nil {
				return err
			}
			if archived {
				filtered := tasks[:0]
				for _, task := range tasks {
					if task.Lifecycle == "archived" {
						filtered = append(filtered, task)
					}
				}
				tasks = filtered
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(tasks)
			}
			if len(tasks) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No tasks. Create one with: maestro new <title>")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "TASK\tSTATE\tAGENT\tBRANCH")
			for _, t := range tasks {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Slug, t.Lifecycle, t.Agent, t.Branch)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include archived tasks")
	cmd.Flags().BoolVar(&archived, "archived", false, "List only archived tasks")
	cmd.MarkFlagsMutuallyExclusive("all", "archived")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	return cmd
}

func effectiveConfig(cmd *cobra.Command) (config.Config, error) {
	repo, _ := git.Discover(cmd.Context(), ".")
	return config.Load(config.DefaultPaths(), repo.Root)
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "config", Short: "Print effective configuration", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := effectiveConfig(cmd)
			if err != nil {
				return err
			}
			return toml.NewEncoder(cmd.OutOrStdout()).Encode(cfg)
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use: "path", Short: "Print the global configuration path", Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), config.DefaultPaths().ConfigFile)
		},
	})
	return cmd
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use: "doctor", Short: "Report installed commands and configuration", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := effectiveConfig(cmd)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			enhanced := false
			probeStatus := "not probed (non-interactive input/output)"
			if output, ok := w.(*os.File); ok && xterm.IsTerminal(output.Fd()) && xterm.IsTerminal(os.Stdin.Fd()) {
				enhanced, err = tui.ProbeKeyboard(cmd.Context(), os.Stdin, w)
				if err != nil {
					return err
				}
				probeStatus = "unsupported"
				if enhanced {
					probeStatus = "supported"
				}
			}
			path, err := exec.LookPath("git")
			if err != nil {
				fmt.Fprintln(w, "git: not found")
			} else {
				fmt.Fprintln(w, "git:", path, commandVersion(cmd.Context(), path))
			}
			names := make([]string, 0, len(cfg.Agents))
			for name := range cfg.Agents {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				path, err := (agent.Generic{Name: name, Config: cfg.Agents[name]}).Detect()
				if err != nil {
					fmt.Fprintf(w, "%s: not found (%s)\n", name, cfg.Agents[name].Cmd)
				} else {
					fmt.Fprintf(w, "%s: %s %s\n", name, path, commandVersion(cmd.Context(), path))
				}
			}
			fmt.Fprintf(w, "config: %s\nworktrees: %s\n", config.DefaultPaths().ConfigFile, cfg.Worktree.Root)
			fmt.Fprintf(w, "keyboard disambiguation: %s\nactive prefix: %s (preferred: %s)\n", probeStatus, term.ActivePrefix(cfg.Prefix, cfg.PrefixFallback, enhanced), cfg.Prefix)
			if cfg.Icons == "nerd" {
				fmt.Fprintln(w, "icons: Unicode fallback (Nerd Font availability cannot be reliably queried)")
			} else {
				fmt.Fprintln(w, "icons:", cfg.Icons)
			}
			return nil
		},
	}
}

func commandVersion(ctx context.Context, command string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, command, "--version").Output()
	if err != nil {
		return "(version unavailable)"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line
}

package cli

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/git"
)

func newThemeCmd() *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use: "theme [name]", Short: "List themes or save a theme choice", Args: cobra.MaximumNArgs(1),
		Long:      "List four built-in themes, or save a choice globally. Use --local for this repository.\nUse auto to follow the terminal background. Restart an open TUI to apply CLI changes;\nprefix T previews and applies a choice inside the TUI.",
		ValidArgs: []string{"dark", "light", "catppuccin", "tokyo-night", "auto"},
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := config.DefaultPaths()
			repo, repoErr := git.Discover(cmd.Context(), ".")
			if local && repoErr != nil {
				return fmt.Errorf("--local requires a Git checkout: %w", repoErr)
			}
			path := paths.ConfigFile
			if local {
				path = filepath.Join(repo.Root, ".maestro.toml")
			}
			if len(args) == 0 {
				cfg, err := config.Load(paths, repo.Root)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Current selection: %s\n\n", cfg.Theme)
				for _, theme := range config.BuiltinThemes() {
					fmt.Fprintf(cmd.OutOrStdout(), "  %-13s %-12s %s\n", theme.ID, theme.Name, theme.Description)
				}
				names := make([]string, 0, len(cfg.Themes))
				for name := range cfg.Themes {
					names = append(names, name)
				}
				sort.Strings(names)
				if len(names) > 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "\nCustom themes are configured in TOML:")
					for _, name := range names {
						fmt.Fprintln(cmd.OutOrStdout(), " ", name)
					}
				}
				fmt.Fprintln(cmd.OutOrStdout(), "\nChoose: maestro theme <name> [--local]\nAutomatic: maestro theme auto\nIn the TUI: prefix T previews themes; Enter saves.")
				return nil
			}
			var override string
			if !local && repo.Root != "" {
				var err error
				override, err = config.FileTheme(filepath.Join(repo.Root, ".maestro.toml"))
				if err != nil {
					return err
				}
			}
			if err := config.SaveTheme(path, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Theme %s saved to %s. Applies on the next TUI launch.\n", args[0], path)
			if override != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "This repository overrides the global theme with %s. Use --local to change it.\n", override)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "Save in the main checkout's .maestro.toml")
	return cmd
}

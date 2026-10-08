package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/forge/github"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Authenticate GitHub commands", Args: cobra.NoArgs}
	cmd.AddCommand(&cobra.Command{
		Use: "login", Short: "Sign in to GitHub using the GitHub CLI", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			login, err := github.LoginCommand(cmd.Context())
			if err != nil {
				return err
			}
			login.Stdin = os.Stdin
			login.Stdout = cmd.OutOrStdout()
			login.Stderr = cmd.ErrOrStderr()
			return login.Run()
		},
	}, &cobra.Command{
		Use: "status", Short: "Check effective GitHub credentials", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := effectiveConfig(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			_, source, err := github.Token(ctx, cfg.GitHub.Token)
			if err != nil {
				return err
			}
			if err := github.New(cfg.GitHub.Token).CheckAuth(ctx); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "GitHub: authenticated (%s)\n", source)
			return nil
		},
	})
	return cmd
}

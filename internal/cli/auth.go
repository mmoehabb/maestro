package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/forge/codeberg"
	"github.com/mmoehabb/maestro/internal/forge/github"
	"github.com/mmoehabb/maestro/internal/forge/gitlab"
	"github.com/mmoehabb/maestro/internal/git"
)

func repositoryAuthHost(ctx context.Context, cfg config.Config) string {
	repo, err := git.Discover(ctx, ".")
	if err == nil {
		host := forge.RemoteHost(repo.Remote)
		if host == "gitlab.com" || (cfg.GitLab.Host != "" && host == cfg.GitLab.Host) {
			return host
		}
		if host == "codeberg.org" || (cfg.Codeberg.Host != "" && host == cfg.Codeberg.Host) {
			return host
		}
	}
	return "github.com"
}

func checkAuth(ctx context.Context, cfg config.Config, host string) (string, error) {
	switch {
	case host == "github.com":
		_, source, err := github.Token(ctx, cfg.GitHub.Token)
		if err == nil {
			err = github.New(cfg.GitHub.Token).CheckAuth(ctx)
		}
		return source, err
	case host == "codeberg.org" || (cfg.Codeberg.Host != "" && host == cfg.Codeberg.Host):
		_, source, err := codeberg.Token(ctx, host, cfg.Codeberg.Token)
		if err == nil {
			err = codeberg.New(host, cfg.Codeberg.Token).CheckAuth(ctx)
		}
		return source, err
	default:
		_, source, err := gitlab.Token(ctx, host, cfg.GitLab.Token)
		if err == nil {
			err = gitlab.New(host, cfg.GitLab.Token).CheckAuth(ctx)
		}
		return source, err
	}
}

func newAuthCmd() *cobra.Command {
	var host string
	cmd := &cobra.Command{Use: "auth", Short: "Authenticate GitHub, GitLab, or Codeberg commands", Args: cobra.NoArgs}
	cmd.PersistentFlags().StringVar(&host, "host", "", "Git host (defaults to the repository's host)")
	cmd.AddCommand(&cobra.Command{Use: "login", Short: "Sign in using gh, glab, or tea", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := effectiveConfig(cmd)
		if err != nil {
			return err
		}
		selected := host
		if selected == "" {
			selected = repositoryAuthHost(cmd.Context(), cfg)
		}
		var login *exec.Cmd
		switch {
		case selected == "github.com":
			login, err = github.LoginCommand(cmd.Context())
		case selected == "codeberg.org" || (cfg.Codeberg.Host != "" && selected == cfg.Codeberg.Host):
			login, err = codeberg.LoginCommand(cmd.Context(), selected)
		default:
			login, err = gitlab.LoginCommand(cmd.Context(), selected)
		}
		if err != nil {
			return err
		}
		login.Stdin, login.Stdout, login.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
		return login.Run()
	}}, &cobra.Command{Use: "status", Short: "Check effective Git host credentials", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := effectiveConfig(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		selected := host
		if selected == "" {
			selected = repositoryAuthHost(ctx, cfg)
		}
		source, err := checkAuth(ctx, cfg, selected)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: authenticated (%s)\n", selected, source)
		return nil
	}})
	return cmd
}

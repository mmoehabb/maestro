package github

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Token never includes credentials or command output in an error.
func Token(ctx context.Context, configured string) (token, source string, err error) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value, name, nil
		}
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if output, e := exec.CommandContext(c, "gh", "auth", "token", "--hostname", "github.com").Output(); e == nil && strings.TrimSpace(string(output)) != "" {
		return strings.TrimSpace(string(output)), "gh auth token", nil
	}
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured, "github.token", nil
	}
	return "", "", fmt.Errorf("GitHub authentication unavailable; run maestro auth login (or set GH_TOKEN)")
}

// LoginCommand uses gh's interactive flow and credential storage.
func LoginCommand(ctx context.Context) (*exec.Cmd, error) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return nil, fmt.Errorf("%s overrides stored GitHub credentials; unset it before running maestro auth login", name)
		}
	}
	path, err := exec.LookPath("gh")
	if err != nil {
		return nil, fmt.Errorf("GitHub CLI (gh) is required for login; install it or set GH_TOKEN")
	}
	return exec.CommandContext(ctx, path, "auth", "login", "--hostname", "github.com"), nil
}

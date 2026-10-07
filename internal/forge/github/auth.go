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
	if configured != "" {
		return configured, "github.token", nil
	}
	return "", "", fmt.Errorf("GitHub authentication unavailable; set GH_TOKEN or run gh auth login")
}

package gitlab

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func Token(ctx context.Context, host, configured string) (string, string, error) {
	for _, name := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token, name, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "glab", "config", "get", "token", "--host", host)
	cmd.Env = append(os.Environ(), "GLAB_CHECK_UPDATE=false", "GLAB_SEND_TELEMETRY=false", "GLAB_NO_PROMPT=true", "NO_COLOR=1")
	if output, err := cmd.Output(); err == nil {
		token := strings.TrimSpace(string(output))
		if token != "" && len(strings.Fields(token)) == 1 {
			return token, "glab config get token", nil
		}
	}
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured, "gitlab.token", nil
	}
	return "", "", fmt.Errorf("GitLab authentication unavailable; run glab auth login --hostname %s or set GITLAB_TOKEN", host)
}

func LoginCommand(ctx context.Context, host string) (*exec.Cmd, error) {
	for _, name := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return nil, fmt.Errorf("%s overrides stored credentials; unset it before login", name)
		}
	}
	path, err := exec.LookPath("glab")
	if err != nil {
		return nil, fmt.Errorf("GitLab CLI (glab) is required for login; install it or set GITLAB_TOKEN")
	}
	return exec.CommandContext(ctx, path, "auth", "login", "--hostname", host), nil
}

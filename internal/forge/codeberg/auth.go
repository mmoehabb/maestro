package codeberg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func Token(ctx context.Context, host, configured string) (string, string, error) {
	if host == "" {
		host = "codeberg.org"
	}
	for _, name := range []string{"CODEBERG_TOKEN", "GITEA_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token, name, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// tea login list displays configured logins, or tea login export outputs tokens.
	// We can use tea login export <host> or tea login export.
	cmd := exec.CommandContext(ctx, "tea", "login", "export", host)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	if output, err := cmd.Output(); err == nil {
		token := strings.TrimSpace(string(output))
		if token != "" && len(strings.Fields(token)) == 1 {
			return token, "tea login export", nil
		}
	}
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured, "codeberg.token", nil
	}
	return "", "", fmt.Errorf("codeberg authentication unavailable; run tea login add --url https://%s or set CODEBERG_TOKEN", host)
}

func LoginCommand(ctx context.Context, host string) (*exec.Cmd, error) {
	if host == "" {
		host = "codeberg.org"
	}
	for _, name := range []string{"CODEBERG_TOKEN", "GITEA_TOKEN"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return nil, fmt.Errorf("%s overrides stored credentials; unset it before login", name)
		}
	}
	path, err := exec.LookPath("tea")
	if err != nil {
		return nil, fmt.Errorf("tea CLI (tea) is required for login; install it or set CODEBERG_TOKEN")
	}
	return exec.CommandContext(ctx, path, "login", "add", "--url", "https://"+host), nil
}

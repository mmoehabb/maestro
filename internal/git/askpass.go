package git

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type credentialPromptKey struct{}

type CredentialPrompt func(context.Context, string) (string, error)

var ErrCredentialCanceled = errors.New("git authentication canceled")

// WithCredentialPrompt routes Git and SSH askpass requests to the caller.
func WithCredentialPrompt(ctx context.Context, prompt CredentialPrompt) context.Context {
	return context.WithValue(ctx, credentialPromptKey{}, prompt)
}

// RunAskpass handles invocations of this executable by Git or OpenSSH. Secrets
// travel over a temporary loopback connection, never arguments or files.
func RunAskpass(args []string, out io.Writer) (handled bool, code int) {
	endpoint := os.Getenv("MAESTRO_ASKPASS_URL")
	if endpoint == "" {
		return false, 0
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || len(args) != 1 {
		return true, 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(args[0]))
	if err != nil {
		return true, 1
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("MAESTRO_ASKPASS_TOKEN"))
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return true, 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return true, 1
	}
	secret, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return true, 1
	}
	if _, err = fmt.Fprintln(out, string(secret)); err != nil {
		return true, 1
	}
	return true, 0
}

func runWithPrompt(ctx context.Context, cmd *exec.Cmd, prompt CredentialPrompt) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("start Git credential prompt: %w", err)
	}
	op, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(op, cmd.Path, cmd.Args[1:]...)
	command.Dir = cmd.Dir
	command.Stdin = cmd.Stdin
	cmd = command
	prepareCredentialCommand(cmd)
	token := rand.Text()
	failure := make(chan error, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		request, stop := context.WithCancel(op)
		defer stop()
		unlink := context.AfterFunc(r.Context(), stop)
		defer unlink()
		secret, err := prompt(request, string(body))
		if err != nil {
			select {
			case failure <- err:
			default:
			}
			cancel()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, secret)
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	// Force SSH to use askpass even while Maestro owns a controlling terminal.
	// https://man.openbsd.org/ssh.1#SSH_ASKPASS_REQUIRE
	cmd.Env = append(os.Environ(), "GIT_ASKPASS="+exe, "SSH_ASKPASS="+exe,
		"SSH_ASKPASS_REQUIRE=force", "GIT_TERMINAL_PROMPT=0",
		"MAESTRO_ASKPASS_URL=http://"+listener.Addr().String()+"/", "MAESTRO_ASKPASS_TOKEN="+token)
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	select {
	case promptErr := <-failure:
		return "", promptErr
	default:
	}
	return string(output), err
}

// PromptCredential forwards an IPC credential challenge to the current caller.
func PromptCredential(ctx context.Context, prompt string) (string, error) {
	if callback, ok := ctx.Value(credentialPromptKey{}).(CredentialPrompt); ok {
		return callback(ctx, prompt)
	}
	return "", fmt.Errorf("git credentials required; authenticate with your credential helper or retry in the TUI")
}

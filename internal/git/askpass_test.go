package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if handled, code := RunAskpass(os.Args[1:], os.Stdout); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestGitAskpassPreservesCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "credential.helper=", "credential", "fill")
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
	secret := " p@ss word!?é "
	var prompts []string
	output, err := runWithPrompt(ctx, cmd, func(_ context.Context, prompt string) (string, error) {
		prompts = append(prompts, prompt)
		if strings.Contains(prompt, "Username") {
			return "user", nil
		}
		return secret, nil
	})
	if err != nil {
		t.Fatalf("credential helper failed: %v: %s", err, output)
	}
	if len(prompts) != 2 || !strings.Contains(output, "username=user\npassword="+secret) {
		t.Fatal("askpass did not preserve username/password")
	}
}

func TestSSHKeyPassphraseAndCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local OpenSSH fixture")
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	key := filepath.Join(t.TempDir(), "encrypted key")
	secret := " p@ss word!?é "
	if err := exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", secret, "-f", key).Run(); err != nil {
		t.Fatal(err)
	}
	for _, canceled := range []bool{false, true} {
		calls := 0
		cmd := exec.CommandContext(ctx, "ssh-keygen", "-y", "-f", key)
		output, err := runWithPrompt(ctx, cmd, func(_ context.Context, prompt string) (string, error) {
			calls++
			if !strings.Contains(strings.ToLower(prompt), "passphrase") {
				t.Error("unexpected SSH prompt")
			}
			if canceled {
				return "", ErrCredentialCanceled
			}
			return secret, nil
		})
		if calls != 1 {
			t.Fatalf("SSH asked %d times", calls)
		}
		if canceled {
			if !errors.Is(err, ErrCredentialCanceled) || output != "" {
				t.Fatalf("unexpected cancellation result: %v", err)
			}
		} else if err != nil || !strings.HasPrefix(output, "ssh-ed25519 ") {
			t.Fatalf("correct passphrase failed: %v", err)
		}
	}
}

func TestCredentialProcessStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "credential.helper=", "credential", "fill")
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := runWithPrompt(ctx, cmd, func(ctx context.Context, _ string) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("credential prompt did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled operation succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("credential subprocess remained stuck after cancellation")
	}
}

// Package notify delivers best-effort desktop alerts for attached and detached runtimes.
package notify

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"
)

type Notifier interface {
	Send(context.Context, string, string) error
}
type Desktop struct{}

func (Desktop) Send(ctx context.Context, title, body string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	name, args, env := command(runtime.GOOS, title, body)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	return cmd.Run()
}

func command(platform, title, body string) (string, []string, []string) {
	switch platform {
	case "darwin":
		return "osascript", []string{"-e", "on run argv\ndisplay notification (item 2 of argv) with title (item 1 of argv)\nend run", "--", title, body}, nil
	case "windows":
		// NotifyIcon works for an unpackaged CLI without registering an AppUserModelID.
		// Keep the helper alive briefly so Windows can display the balloon/toast.
		// https://learn.microsoft.com/dotnet/api/system.windows.forms.notifyicon.showballoontip
		// Content is environment data, never interpolated into PowerShell.
		script := `$ErrorActionPreference = 'Stop'; Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $icon = New-Object System.Windows.Forms.NotifyIcon; try { $icon.Icon = [System.Drawing.SystemIcons]::Information; $icon.Text = 'Maestro'; $icon.Visible = $true; $icon.ShowBalloonTip(5000, $env:MAESTRO_NOTIFY_TITLE, $env:MAESTRO_NOTIFY_BODY, [System.Windows.Forms.ToolTipIcon]::Info); Start-Sleep -Milliseconds 5500 } finally { $icon.Dispose() }`
		return "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", script}, []string{"MAESTRO_NOTIFY_TITLE=" + title, "MAESTRO_NOTIFY_BODY=" + body}
	default:
		return "notify-send", []string{"--app-name=Maestro", "--", title, body}, nil
	}
}

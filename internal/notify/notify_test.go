package notify

import (
	"strings"
	"testing"
)

func TestNotificationContentIsData(t *testing.T) {
	title := `title "; $(touch bad) <script>`
	body := "body\n' & < >"
	for _, platform := range []string{"linux", "darwin", "windows"} {
		name, args, env := command(platform, title, body)
		if name == "" {
			t.Fatal("missing command")
		}
		if platform == "windows" {
			if strings.Contains(args[len(args)-1], title) || len(env) != 2 || env[0] != "MAESTRO_NOTIFY_TITLE="+title || env[1] != "MAESTRO_NOTIFY_BODY="+body {
				t.Fatal("content interpolated into script")
			}
		} else if args[len(args)-2] != title || args[len(args)-1] != body {
			t.Fatal("content was not passed as literal argv")
		}
	}
}

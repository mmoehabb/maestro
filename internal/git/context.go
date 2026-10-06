package git

import (
	"context"
	"fmt"
	"strings"
)

func Context(ctx context.Context, path, base string) (string, error) {
	var b strings.Builder
	for _, q := range []struct {
		title string
		args  []string
	}{
		{"Commits", []string{"log", "--oneline", base + "..HEAD", "--"}},
		{"Changes against base", []string{"diff", "--stat", base, "--"}},
		{"Uncommitted files", []string{"status", "--short"}},
	} {
		out, err := run(ctx, path, q.args...)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "### %s\n%s\n", q.title, out)
	}
	return b.String(), nil
}

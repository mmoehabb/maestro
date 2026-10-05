package git

import (
	"context"
	"strconv"
	"strings"
)

type Status struct {
	Dirty, Added, Deleted, Ahead, Behind, Commits int
	Upstream                                      bool
}

func WorktreeStatus(ctx context.Context, path, base string) (Status, error) {
	var s Status
	out, err := run(ctx, path, "status", "--porcelain=v1", "-z")
	if err != nil {
		return s, err
	}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		if len(entries[i]) < 3 {
			continue
		}
		s.Dirty++
		if strings.ContainsAny(entries[i][:2], "RC") {
			i++
		}
	}
	out, err = run(ctx, path, "diff", "--numstat", "HEAD", "--")
	if err != nil {
		return s, err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		a, _ := strconv.Atoi(fields[0])
		d, _ := strconv.Atoi(fields[1])
		s.Added += a
		s.Deleted += d
	}
	out, err = run(ctx, path, "rev-list", "--count", base+"..HEAD", "--")
	if err != nil {
		return s, err
	}
	s.Commits, _ = strconv.Atoi(out)
	out, err = run(ctx, path, "rev-list", "--left-right", "--count", "HEAD...@{upstream}", "--")
	if err == nil {
		s.Upstream = true
		fields := strings.Fields(out)
		if len(fields) == 2 {
			s.Ahead, _ = strconv.Atoi(fields[0])
			s.Behind, _ = strconv.Atoi(fields[1])
		}
	} else {
		s.Ahead = s.Commits
	}
	return s, nil
}

// Package version exposes build metadata injected at link time via -ldflags.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set by goreleaser / make via -ldflags "-X github.com/mmoehabb/maestro/internal/version.Version=...".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Info is a snapshot of build metadata.
type Info struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
	Platform  string
}

// Get returns build info, falling back to VCS data embedded by the Go toolchain.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		info.Version = moduleVersion(info.Version, bi.Main.Version)
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			}
		}
	}
	if len(info.Commit) > 12 {
		info.Commit = info.Commit[:12]
	}
	return info
}

func moduleVersion(version, module string) string {
	if version == "dev" && module != "" && module != "(devel)" {
		return module
	}
	return version
}

// String renders a one-line human-readable version.
func (i Info) String() string {
	s := "maestro " + i.Version
	if i.Commit != "" {
		s += " (" + i.Commit
		if i.Date != "" {
			s += ", " + i.Date
		}
		s += ")"
	}
	return fmt.Sprintf("%s %s %s", s, i.GoVersion, i.Platform)
}

package core_test

import (
	"os"
	"strings"
	"testing"
)

// configureFakeAgentRace removes the per-process exit delay from fake agents.
// The parent race runtime has already initialized; only future children use this
// environment setting. Race detection and all other GORACE options stay enabled.
func configureFakeAgentRace(t *testing.T) {
	t.Helper()
	options := strings.Fields(os.Getenv("GORACE"))
	filtered := options[:0]
	for _, option := range options {
		if !strings.HasPrefix(option, "atexit_sleep_ms=") {
			filtered = append(filtered, option)
		}
	}
	filtered = append(filtered, "atexit_sleep_ms=0")
	t.Setenv("GORACE", strings.Join(filtered, " "))
}

package agent

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/mmoehabb/maestro/internal/config"
)

var ErrUnsupported = errors.New("native transcript unavailable; using terminal history")

// ParserVersion changes when the normalized interpretation of source records changes.
const ParserVersion = 2

type Record struct {
	Key, Role, Content, ToolCallID string
	TS                             time.Time
}
type TurnEvent struct {
	Key, Kind string // started, done, interrupted
	TS        time.Time
}
type Transcript struct {
	Incomplete bool // A trailing partial record has not yet been imported.
	Records    []Record
	Events     []TurnEvent
	Path       string
}
type Adapter interface {
	ID() string
	Detect() (string, error)
	Command(context.Context, LaunchSpec) (*exec.Cmd, error)
	CreateSession(context.Context, string) (string, error)
	DiscoverSession(context.Context, string, time.Time) (string, error)
	Hints() []string
	Read(context.Context, string, string) (Transcript, error)
}
type Registry struct{ Agents map[string]config.Agent }

func (r Registry) Get(name string) (Adapter, error) {
	cfg, ok := r.Agents[name]
	if !ok {
		return nil, errors.New("agent is no longer configured: " + name)
	}
	return nativeAdapter{Generic: Generic{Name: name, Config: cfg}}, nil
}

type nativeAdapter struct{ Generic }

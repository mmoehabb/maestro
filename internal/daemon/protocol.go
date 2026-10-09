// Package daemon owns agent runtimes independently of terminal clients.
package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

const (
	ProtocolVersion = 1
	maxMessage      = 24 << 20
)

// Endpoint is stable across linked worktrees and isolated across data directories.
func Endpoint(dataDir, key string) (string, error) {
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absolute + "\x00" + key))
	return endpoint(fmt.Sprintf("%x", sum[:16]))
}

type request struct {
	Version         int
	Method, Lease   string
	Args            core.Request
	Key             uv.Key
	Mouse           uv.Mouse
	Release, Motion bool
}
type response struct {
	Error, Code, Prompt string
	Data                json.RawMessage
}
type paneState struct {
	TaskID, Generation int64
	Snapshot           term.Snapshot
	InputError         string
}
type syncState struct {
	Panes   []paneState
	Tasks   []store.Task
	Cleanup []int64
	Error   string
}
type Status struct {
	PID      int
	Attached bool
	Agents   int
	Version  int
}

func encode(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > maxMessage {
		return fmt.Errorf("daemon message exceeds 24 MiB")
	}
	// JSON is newline framed; Decoder on a limited reader bounds every request.
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func decode(r io.Reader, value any) error {
	return json.NewDecoder(io.LimitReader(r, maxMessage)).Decode(value)
}

func result(value any, err error) response {
	if err != nil {
		code := ""
		if errors.Is(err, core.ErrFreshStartRequired) {
			code = "fresh"
		}
		if errors.Is(err, core.ErrInterruptRequired) {
			code = "busy"
		}
		return response{Error: err.Error(), Code: code}
	}
	data, e := json.Marshal(value)
	if e != nil {
		return response{Error: e.Error()}
	}
	return response{Data: data}
}

func responseError(r response) error {
	if r.Error == "" {
		return nil
	}
	switch r.Code {
	case "fresh":
		return fmt.Errorf("%s: %w", r.Error, core.ErrFreshStartRequired)
	case "busy":
		return fmt.Errorf("%s: %w", r.Error, core.ErrInterruptRequired)
	}
	return errors.New(r.Error)
}

type Remote struct {
	Address string
	Lease   string
}

func (r Remote) Call(ctx context.Context, method string, args core.Request, out any) error {
	return r.call(ctx, request{Method: method, Args: args}, out)
}

func (r Remote) call(ctx context.Context, req request, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	conn, err := dial(ctx, r.Address)
	if err != nil {
		return fmt.Errorf("connect to Maestro daemon: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	req.Version, req.Lease = ProtocolVersion, r.Lease
	if err = encode(conn, req); err != nil {
		return err
	}
	// Responses are read one at a time; the server waits for a credential reply.
	for {
		var reply response
		if err = decode(conn, &reply); err != nil {
			return fmt.Errorf("daemon connection lost (operation may have completed; inspect state before retrying): %w", err)
		}
		if reply.Prompt != "" {
			value, e := git.PromptCredential(ctx, reply.Prompt)
			answer := response{Data: json.RawMessage("null")}
			if e != nil {
				answer.Error = e.Error()
			} else {
				answer.Data, _ = json.Marshal(value)
			}
			if err = encode(conn, answer); err != nil {
				return err
			}
			continue
		}
		if err = responseError(reply); err != nil {
			return err
		}
		if out != nil {
			return json.Unmarshal(reply.Data, out)
		}
		return nil
	}
}

func Probe(ctx context.Context, address string) (Status, error) {
	var status Status
	err := (Remote{Address: address}).Call(ctx, "status", core.Request{}, &status)
	return status, err
}
func connection(ctx context.Context, address string) (net.Conn, error) { return dial(ctx, address) }

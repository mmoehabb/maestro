# Maestro

> **tmux for coding agents.** Run codex, agy, opencode and friends side by side in tabs. Each tab is a task with its own git worktree that becomes a PR, and Maestro keeps the history so you can switch agents without losing context.

> [!NOTE]
> P1 is implemented: isolated worktrees, embedded agent terminals, tabs, activity states, persistence and session resume. Context handoff and forge operations arrive in P2/P3. See [docs/PLAN.md](docs/PLAN.md).

## Install (from source)

```bash
go install github.com/mmoehabb/maestro/cmd/maestro@latest
```

## Development

Requires Go 1.26+ (see `go.mod`). Optional: [golangci-lint](https://golangci-lint.run) v2.9.0+ built with a compatible Go version, [goreleaser](https://goreleaser.com) v2.

```bash
make build      # bin/maestro
make test       # go test -race ./...
make lint       # run the pinned Go-compatible golangci-lint
make snapshot   # local cross-platform release build into dist/
```

`make lint` and `make fmt` fetch the pinned linter through Go without replacing an installed binary. Set `GOLANGCI_LINT=golangci-lint` to use your own compatible installation.

## Available now

Run inside a Git checkout with at least one commit:

```bash
maestro new "Fix login" -a codex -b main -p "Repair the login flow"
maestro               # restore all tasks and start/resume their agents
maestro open fix-login # restore tabs, focused on this task
maestro ls
maestro ls --all --json
maestro config         # print effective configuration
maestro config path   # print global config file location
maestro doctor        # executable paths, versions, keyboard probe and prefix
```

`new` provisions a task without opening the TUI, so it also works in scripts. `maestro` and `maestro open` launch all unarchived task tabs. Creating a task inside the TUI immediately launches its agent. A failed agent launch stays visible with retry instructions. Quitting stops the agents and saves session identity, exit status and terminal fallback history; this is not a daemon.

The prefix is `ctrl+m` after the terminal confirms keyboard disambiguation, otherwise `ctrl+b`. Enter always reaches the agent. Use `prefix ?` for help:

| Keys | Action |
|---|---|
| `alt+1…9`, `alt+h` / `alt+l` | Select, previous / next tab |
| `prefix c` | New task: title, base branch, agent and prompt |
| `prefix x`, `prefix r` | Stop, restart/resume the agent |
| `prefix R` | Explicitly start a fresh session |
| `prefix [` | Scroll history with j/k, arrows and page keys; y copies history |
| `prefix prefix` | Send the prefix itself to the agent |
| `prefix q` | Stop agents, save and quit |

Click tabs to switch. Mouse input within the terminal is forwarded; the wheel enters Maestro's scrollback. Background panes keep processing output. Runtime icons show Starting, Working, Done, NeedsInput, Exited and Crashed. Done uses terminal notifications or the configurable quiet timer; it is a heuristic, not proof that a long-running tool has finished. Prompt hints also use heuristics. `icons = "nerd"` conservatively uses Unicode glyphs because terminal glyph width does not reliably identify installed fonts; `ascii` uses ASCII runtime icons.

### Session resume

Maestro saves native session IDs during execution and resumes by ID without replaying the first prompt. Minimal identity discovery is included for Codex rollout metadata, agy's workspace-to-conversation cache, and OpenCode's JSON session listing. These private formats can change; full transcript adapters and context transfer remain P2. If an existing resumable task has no discoverable ID, Maestro shows an error and offers `prefix R` for an explicit fresh start.

Custom agents can write their ID to a worktree-relative `session_file`, or opt into a generated UUID if they support client-supplied IDs:

```toml
[agents.custom]
cmd = "my-agent"
generate_session_id = true
new = ["--session-id", "{{.SessionID}}", "{{.Prompt}}"]
resume = ["--resume", "{{.SessionID}}"]
input_hints = ["Approve?"]
# Alternative to generate_session_id: session_file = ".maestro/session-id"
```

Argument templates render directly into argv; empty arguments are dropped and prompts are never interpreted by a shell. `MAESTRO_TASK` and `MAESTRO_SESSION_ID` are also available to the child process. Stateless custom agents with no resume arguments restart as new processes.

Configuration layers are built-in defaults, the global config file, then the main checkout's `.maestro.toml`. The `new` flags override the corresponding task choices. Agent tables merge by key, so overriding `cmd` preserves the default argument templates. See [the default config](internal/config/defaults.toml) for supported settings.

Data lives in the platform's XDG data directory under `maestro`: `maestro.db`, `locks/`, and `worktrees/<repository-key>/<task-slug>/`. The repository key hashes the Git common directory, keeping checkouts with identical names separate. Commands run from a linked worktree resolve to the same project as the main checkout. The TUI holds the project lock for its lifetime; CLI listing remains available. A second TUI or external task-creation command reports that the project is busy.

`worktree.copy` copies regular files only, skips missing files, and rejects symlinks, traversal, Git metadata and existing destinations. Copied files have owner-only permissions. `worktree.setup` runs trusted shell commands from your config in each new worktree (`sh` on Unix, `cmd` on Windows). Review repository config before using it. If copying, setup or database persistence fails after worktree creation, Maestro retains the checkout and reports its path and branch. Inspect and recover it manually before retrying; failed provisioning is not listed as a saved task. No automatic cleanup is implemented yet.

### Verification

The test suite includes real PTY processes and a three-task stop/reopen/resume integration test, plus UI snapshots at 80×24 and 160×48. Regenerate snapshots with `go test ./internal/tui -update` and review the diff. A fake agent is available with `go build -o /tmp/maestro-fakeagent ./internal/testutil/fakeagent`; configure `new = ["new", "{{.SessionID}}"]`, `resume = ["resume", "{{.SessionID}}"]` and `generate_session_id = true` to try it without API credentials.

Linux terminal smoke tests and Windows/macOS cross-builds have been performed. Native Windows/macOS behavior and authenticated real-agent conversations still need manual verification. GitHub auth diagnostics are part of P3.

### Layout

| Path | Purpose |
|---|---|
| `cmd/maestro` | Binary entry point |
| `internal/cli` | Cobra command tree |
| `internal/version` | Build metadata (set via `-ldflags`) |
| `internal/{app,config,store,core,git,agent,term,tui}` | P1: MVP |
| `internal/handoff` | P2: context handoff between agents |
| `internal/forge` | P3: GitHub PRs, CI, merges |

## License

[MIT](LICENSE)

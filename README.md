# Maestro

> **tmux for coding agents.** Run codex, agy, opencode and friends side by side in tabs. Each tab is a task with its own git worktree that becomes a PR, and Maestro keeps the history so you can switch agents without losing context.

> [!NOTE]
> Early development. The CLI skeleton exists; the TUI arrives in P1. See the roadmap in [docs/PLAN.md](docs/PLAN.md).

## Install (from source)

```bash
go install github.com/mmoehabb/maestro/cmd/maestro@latest
```

## Development

Requires Go 1.24+. Optional: [golangci-lint](https://golangci-lint.run) v2, [goreleaser](https://goreleaser.com) v2.

```bash
make build      # bin/maestro
make test       # go test -race ./...
make lint       # golangci-lint run
make snapshot   # local cross-platform release build into dist/
```

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

# Repository Guidelines

## Project Structure & Module Organization

Maestro is a Go CLI and terminal UI for managing coding agents in isolated Git worktrees.

- `cmd/maestro/`: executable entry point.
- `internal/cli/` and `internal/tui/`: Cobra commands and Bubble Tea interface.
- `internal/core/`: task orchestration and lifecycle; `internal/agent/`, `internal/term/`, and `internal/handoff/`: agent integration, terminals, and context transfer.
- `internal/store/`: SQLite persistence, with numbered SQL migrations in `migrations/`.
- `internal/git/`, `internal/forge/`, and `internal/config/`: Git operations, GitHub integration, and configuration.
- Tests live beside source as `*_test.go`; fixtures and snapshots use `testdata/`. Shared helpers live in `internal/testutil/`.
- `docs/` contains design and usage documentation. `website/` contains the static landing page and assets, with no build dependencies.

## Build, Test, and Development Commands

Use Go 1.26 or newer.

- `make build`: build `bin/maestro` with version metadata.
- `make run ARGS="ls"`: build and run a CLI command. Use a Git checkout with at least one commit for task workflows.
- `make test`: run all tests with race detection and caching disabled.
- `make lint`: run pinned golangci-lint v2.9.0.
- `make fmt`: format Go code with gofumpt and goimports.
- `make snapshot`: create local release builds using GoReleaser v2.
- `python3 -m http.server 8000 --directory website`: preview the website.

## Coding Style & Naming Conventions

Use formatter-managed Go indentation (tabs), lowercase package names, and descriptive exported identifiers. Keep imports grouped with the module's imports treated as local. Follow adjacent filename conventions, including `_unix.go` and `_windows.go` for platform implementations. Address lint findings using `.golangci.yml` as the source of truth.

## Testing Guidelines

Use Go's `testing` package and descriptive `TestBehavior` names. TUI tests also use teatest and golden snapshots; review snapshot changes visually. Reuse temporary repositories and fake agents from `internal/testutil/`.

Run focused checks with `go test ./internal/core -run TestName`, then `make test`. CI also verifies modules, builds, vets, checks module tidiness, and tests Linux, macOS, and Windows. No numeric coverage threshold is configured. Consult `docs/P4_UX.md` for manual UI checks.

## Commit & Pull Request Guidelines

Follow the observed Conventional Commit style: `fix: report installed module version` or `fix(tui): address P4 review findings`. Keep commits focused. PR descriptions should explain the problem, resulting behavior, and validation; link relevant issues and include screenshots for visible TUI or website changes. Update usage documentation when behavior changes.

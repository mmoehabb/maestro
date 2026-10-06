# Maestro

> **tmux for coding agents.** Run Codex, agy, OpenCode, Claude Code, Qoder, Kimi Code and Cursor Agent side by side in tabs. Each tab is a task with its own git worktree that becomes a PR, and Maestro keeps the history so you can switch agents without losing context.

> [!NOTE]
> P1 and P2 are implemented: isolated worktrees, embedded agent terminals, session resume, native conversation history and context handoff. Forge operations arrive in P3. See [docs/PLAN.md](docs/PLAN.md).

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
maestro switch fix-login -a agy # open the TUI and hand off this task
maestro history fix-login
maestro history fix-login --json
maestro config         # print effective configuration
maestro config path   # print global config file location
maestro doctor        # executable paths, versions, keyboard probe and prefix
```

`new` provisions a task without opening the TUI, so it also works in scripts. `maestro` and `maestro open` launch all unarchived task tabs. Creating a task inside the TUI immediately launches its agent. A failed agent launch stays visible with retry instructions. Quitting stops the agents and saves session identity, exit status and terminal fallback history; this is not a daemon.

The prefix is `ctrl+m` after the terminal confirms keyboard disambiguation, otherwise `ctrl+b`. Enter always reaches the agent. Use `prefix ?` for help:

| Keys | Action |
|---|---|
| `alt+1…9`, `alt+h` / `alt+l` | Select, previous / next tab |
| `prefix a` | Switch agents with context handoff |
| `prefix h` | Task timeline and expandable conversation history |
| `prefix H` | Copy the handoff instruction for manual-prompt agents |
| `prefix c` | New task: title, base branch, agent and prompt |
| `prefix x`, `prefix r` | Stop, restart/resume the agent |
| `prefix R` | Explicitly start a fresh session |
| `prefix [` | Scroll history with j/k, arrows and page keys; y copies history |
| `prefix prefix` | Send the prefix itself to the agent |
| `prefix q` | Stop agents, save and quit |

Click tabs to switch. Mouse input within the terminal is forwarded; the wheel enters Maestro's scrollback. Background panes keep processing output. Runtime icons show Starting, Working, Done, NeedsInput, Exited and Crashed. Done uses native turn signals when available. A recognized native turn stays Working through silent tool execution; terminal notifications and the configurable quiet timer serve as fallbacks when native monitoring is unavailable. These fallbacks are heuristics, not proof that a long-running tool has finished. Prompt hints also use heuristics. `icons = "nerd"` conservatively uses Unicode glyphs because terminal glyph width does not reliably identify installed fonts; `ascii` uses ASCII runtime icons.

### Session resume

Built-in presets are available in the new-task picker and through `maestro new -a <agent>`:

| Agent key | Executable | Session identity |
|---|---|---|
| `codex` | `codex` | Discovered from rollout metadata |
| `agy` | `agy` | Discovered from the workspace-to-conversation cache |
| `opencode` | `opencode` | Discovered from its JSON session listing |
| `claude` | `claude` | Maestro supplies a UUID using `--session-id` |
| `qoder` | `qoder` | Maestro supplies a UUID using `--session-id` |
| `kimi` | `kimi` | Discovered from Kimi Code's `~/.kimi-code/sessions` metadata |
| `cursor-agent` | `cursor-agent` | Allocated by `cursor-agent create-chat` before launch |

Install and authenticate the chosen CLI separately; `maestro doctor` checks executable availability. Override the preset's `cmd` if your installation uses another executable name, such as `agent` for Cursor or `qodercli` for older Qoder installations. The argument flags must also be supported by that version.

Claude and Cursor accept an initial prompt while remaining interactive. Qoder uses [`--prompt-interactive`](https://docs.qoder.com/cli/cli-reference). Kimi's [`--prompt`](https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/reference/kimi-command.md) runs non-interactively with automatic approvals, so its preset requires manual prompt entry: omit Maestro's `-p` (leave the dialog's first prompt blank) and enter the prompt in the agent pane. Maestro rejects launch-time prompts for this preset before creating a worktree. The Kimi preset targets the current Kimi Code CLI and its `.kimi-code` session layout, not the older Python CLI's `.kimi` storage. Cursor session allocation uses its documented [`create-chat`](https://cursor.com/docs/cli/reference/parameters) command.

Maestro saves native session IDs and resumes by ID without replaying the first prompt. Codex, agy and OpenCode also import native messages and tool activity. Private formats can change; parser failures retain existing history and fall back to terminal capture. If an existing resumable task has no discoverable ID, Maestro shows an error and offers `prefix R` for an explicit fresh start.

Custom agents can write their ID to a worktree-relative `session_file`, or opt into a generated UUID if they support client-supplied IDs:

```toml
[agents.custom]
cmd = "my-agent"
generate_session_id = true
new = ["--session-id", "{{.SessionID}}", "{{.Prompt}}"]
resume = ["--resume", "{{.SessionID}}", "{{.Prompt}}"]
input_hints = ["Approve?"]
# Alternative to generate_session_id: session_file = ".maestro/session-id"
```

Argument templates render directly into argv; empty arguments are dropped and prompts are never interpreted by a shell. `MAESTRO_TASK` and `MAESTRO_SESSION_ID` are also available to the child process. Stateless custom agents with no resume arguments restart as new processes.

Agents that allocate IDs through a separate command can instead use `session_create = ["create-chat"]`. Maestro runs those literal arguments with the configured executable in the task worktree before the first launch, captures the single ID from stdout, and supplies it as `{{.SessionID}}`. It does not run that command when resuming. This setting requires resume arguments and cannot be combined with `generate_session_id` or `session_file`.

Set `manual_prompt = true` when an agent cannot accept an initial prompt in interactive mode. This rejects launch-time prompts with instructions to enter them in the pane.

Configuration layers are built-in defaults, the global config file, then the main checkout's `.maestro.toml`. The `new` flags override the corresponding task choices. Agent tables merge by key, so overriding `cmd` preserves the default argument templates. See [the default config](internal/config/defaults.toml) for supported settings.

Data lives in the platform's XDG data directory under `maestro`: `maestro.db`, `locks/`, and `worktrees/<repository-key>/<task-slug>/`. The repository key hashes the Git common directory, keeping checkouts with identical names separate. Commands run from a linked worktree resolve to the same project as the main checkout. The TUI holds the project lock for its lifetime; CLI listing and history remain available. A second TUI or external task-creation command reports that the project is busy.

`worktree.copy` copies regular files only, skips missing files, and rejects symlinks, traversal, Git metadata and existing destinations. Copied files have owner-only permissions. `worktree.setup` runs trusted shell commands from your config in each new worktree (`sh` on Unix, `cmd` on Windows). Review repository config before using it. If copying, setup or database persistence fails after worktree creation, Maestro retains the checkout and reports its path and branch. Inspect and recover it manually before retrying; failed provisioning is not listed as a saved task. No automatic cleanup is implemented yet.

### Context handoff and history

Use `prefix a` to choose an installed agent; the picker marks agents that previously
worked on the task. Switching away from an active or approval-waiting agent asks
before interruption. Maestro stops the old process, saves its final transcript,
and writes `.maestro/handoff.md` in the same worktree before launching the new
agent. Returning to an agent resumes that agent's own native session ID.

`maestro switch <task> -a <agent>` opens the focused TUI, restores other task tabs,
and performs the same handoff. It needs an interactive terminal. An already-open
TUI owns the project lock, so use its switch dialog instead of a second CLI process.

Handoffs include the goal, notes, agent timeline, explicit TODO mentions, recent
conversation and Git state. Set `[handoff] token_budget = 6000` to adjust the
budget. The deterministic estimator conservatively counts each UTF-8 byte as a
token; actual model token usage is usually lower. Older dialogue and tool output
are trimmed, with omission markers. Terminal snapshots retain their newest lines
and remain in the handoff when native history could not be fully imported.
No model calls are made to summarize history.
Switching validates that the selected new/resume template includes the full
handoff instruction in its rendered arguments before stopping the current agent.
If an agent cannot accept prompts on launch, configure `manual_prompt = true`.
Manual-prompt agents start without injected input; use `prefix H` to copy the
instruction, then paste it into their pane.

A pending handoff is stored in SQLite before launch. If writing the file or
starting the new process fails, retry with `prefix r`; the saved handoff is reused.
If the selected target has no discoverable resume identity, the switch dialog
asks before stopping the current agent and starting the target fresh with a
handoff. This also applies to `maestro switch`. Use `prefix R` to start the
current agent fresh. Worktree/branch cleanup remains P3.

`prefix h` opens history: Tab selects a session, Enter expands it, `t` reveals tool
details, arrows/j/k and page keys scroll, `r` refreshes, and Escape returns to the
pane. Terminal fallback snapshots are labeled `terminal`. `maestro history <task>
--json` returns `task`, `sessions`, `turns`, `events` and `handoffs`. Native records
are deduplicated across resumed launches. Persisted history remains available
from the main repository even after the worktree or agent's source logs disappear.

Native parser coverage:

| Agent | History | Completion evidence |
|---|---|---|
| Codex | Rollout messages and tool records | Task start/complete and interruption events |
| agy | Transcript user/planner/tool steps | Completed textual planner response without tool calls; individual step `DONE` is insufficient |
| OpenCode | Exported messages and parts; read-only SQLite fallback | Completed final assistant response, excluding tool-call continuation steps |
| Other configured agents | Terminal fallback | Notifications and quiet timer |

Internal reasoning and system/developer instructions are not imported. JSONL
files are watched with periodic reconciliation; OpenCode exports are polled once
per second. If its CLI produces no usable JSON, Maestro reads the known native
SQLite schema in read-only mode, scoped to the task workspace and session. This
also supports session discovery when the local CLI listing fails. An unknown
schema still falls back to terminal history. The parsers are versioned with synthetic fixtures because native
formats are not stable public APIs.

### Verification

The test suite includes real PTY processes, three-task restoration, a controlled Codex → agy → Codex handoff, pending-switch recovery, P1 database migration, duplicate-free transcript imports, native activity precedence, retained history after worktree deletion, and UI snapshots at 80×24 and 160×48. Regenerate snapshots with `go test ./internal/tui -update` and review the diff. A fake agent is available with `go build -o /tmp/maestro-fakeagent ./internal/testutil/fakeagent`; configure `new = ["new", "{{.SessionID}}"]`, `resume = ["resume", "{{.SessionID}}"]` and `generate_session_id = true` to try it without API credentials.

Linux terminal smoke tests and Windows/macOS cross-builds have been performed. Read-only parser checks also exercise existing local Codex/agy transcripts and OpenCode native history. Native Windows/macOS behavior and authenticated live cross-agent conversations still need manual verification. GitHub auth diagnostics are part of P3.

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

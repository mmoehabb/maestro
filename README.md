# Maestro

**tmux for coding agents.** Run Codex, agy, OpenCode, Claude Code, Qoder, Kimi Code and Cursor Agent side by side in tabs. Each tab is a task with its own git worktree that becomes a PR, and Maestro keeps the history so you can switch agents without losing context.

## Install (from source)

```bash
go install github.com/mmoehabb/maestro/cmd/maestro@latest
```

## Development

### Product website

The static landing page lives in [`website/`](website/). It uses plain HTML, CSS,
and JavaScript with local assets and no build step or package dependencies.
Preview it from the repository root:

```bash
python3 -m http.server 8000 --directory website
```

Open <http://localhost:8000>. The terminal preview contains illustrative sessions;
its tabs demonstrate the workflow without running real agents.

To publish, set **Settings → Pages → Build and deployment → Source** to
**GitHub Actions** in the GitHub repository. The
[`pages.yml`](.github/workflows/pages.yml) workflow deploys only `website/` when
website files or the workflow change on `main`. You can also run it manually
from the Actions tab on `main`. It uses the built-in `GITHUB_TOKEN`; no custom
deployment secret is needed. The expected project URL is
<https://mmoehabb.github.io/maestro/>. Relative asset paths also support a custom
domain without code changes.

### Application

Requires Go 1.26+ (see `go.mod`). Optional: [golangci-lint](https://golangci-lint.run) v2.9.0+ built with a compatible Go version, [goreleaser](https://goreleaser.com) v2.

```bash
make build      # bin/maestro and bin/maestrod
make test       # go test -race ./...
make lint       # run the pinned Go-compatible golangci-lint
make snapshot   # local cross-platform release build into dist/
```

`make lint` and `make fmt` fetch the pinned linter through Go without replacing an installed binary. Set `GOLANGCI_LINT=golangci-lint` to use your own compatible installation.

## Available now

Run inside a Git checkout with at least one commit:

```bash
maestro new "Fix login" -a codex -b main -p "Repair the login flow"
maestro               # start/connect to the daemon and attach to task agents
maestro attach        # reconnect to existing agent processes
maestro daemon status
maestro stop fix-login # stop one agent and save its history
maestro daemon stop   # stop all project agents and shut down the daemon
maestro rename fix-login "Repair login" # edit the display title
maestro open fix-login # restore tabs, focused on this task
maestro ls
maestro ls --all --json
maestro tabs --archived # list archived tabs (tabs is an alias for ls)
maestro archive fix-login
maestro reopen fix-login
maestro rm fix-login    # delete an archived task and its saved history
maestro switch fix-login -a agy # open the TUI and hand off this task
maestro history fix-login
maestro history fix-login --json
maestro config         # print effective configuration
maestro config path   # print global config file location
maestro doctor        # executable paths, versions, keyboard probe and prefix
```

`new` provisions a task without opening the TUI, so it also works in scripts. `maestro` and `maestro open` launch all unarchived task tabs. Creating a task inside the TUI immediately launches its agent. A failed agent launch stays visible with retry instructions. Closing the TUI or pressing `prefix q` detaches while agents continue running. `maestro daemon stop` stops agents, saves their final history, and shuts down the project daemon.

The prefix is `ctrl+m` after the terminal confirms keyboard disambiguation, otherwise `ctrl+b`. Enter always reaches the agent. Use `prefix ?` for help:

| Keys | Action |
|---|---|
| `alt+1…9`, `alt+h` / `alt+l` | Select, previous / next tab |
| `prefix :` | Search actions and tasks |
| `prefix D` | View diff against the task base |
| `prefix s` | Toggle tabs / sidebar |
| `prefix T` | Preview and save a color theme |
| `prefix a` | Switch agents with context handoff |
| `prefix h` | Task timeline and expandable conversation history |
| `prefix H` | Copy the handoff instruction for manual-prompt agents |
| `prefix n` | Edit task notes (Ctrl+S saves, Esc cancels) |
| `prefix ,` | Rename the task title (Ctrl+S saves, Esc cancels) |
| `prefix C` | Stop the agent and save a portable task checkpoint |
| `prefix c` | New task: title, base branch, agent and prompt |
| `prefix d` | Hide the current tab and stop its agent |
| `prefix g` | Sign in to the repository’s GitHub/GitLab host |
| `prefix p`, `prefix P`, `prefix m` | Push, create/open PR, merge PR |
| `prefix &`, `prefix u` | Archive/clean up, reopen an archived task |
| `prefix x`, `prefix r` | Stop, restart/resume the agent |
| `prefix R` | Explicitly start a fresh session |
| `prefix t` | Open shell in task worktree |
| `prefix z` | Suspend the maestro app |
| `prefix [` | Scroll history with j/k, arrows and page keys; y copies history |
| `prefix e` | Open default/configured editor in current worktree |
| `prefix prefix` | Send the prefix itself to the agent |
| `prefix q` | Detach; agents keep running |

Click tabs to switch or drag to reorder. Mouse input within the terminal is forwarded; the wheel enters Maestro's scrollback. Background panes keep processing output. Runtime icons show Starting, Working, Done, NeedsInput, Exited and Crashed. Done uses native turn signals when available. A recognized native turn stays Working through silent tool execution; terminal notifications and the configurable quiet timer serve as fallbacks when native monitoring is unavailable. These fallbacks are heuristics, not proof that a long-running tool has finished. Prompt hints also use heuristics. `icons = "nerd"` conservatively uses Unicode glyphs because terminal glyph width does not reliably identify installed fonts; `ascii` uses ASCII runtime icons.

### Background agents and task titles

Maestro automatically starts a per-project daemon using its own executable;
release archives also include `maestrod` for foreground supervision. No service
installation is required. Unix uses an owner-only socket; Windows uses a named
pipe restricted to the current user. Linked worktrees share the same daemon.

Detach with `prefix q`, then run `maestro attach` or `maestro open <task>` to
restore terminal screens, scrollback, and status without restarting agents.
Input accepted before detach is drained. Stop individual agents with `prefix x`
or `maestro stop <task>`. `maestro daemon stop` shuts down all agents in this project.
The daemon stays running until explicitly stopped; restarting it reloads configuration
and resumes saved native sessions when the TUI opens. Environment changes also
require a daemon restart. A daemon or machine crash can lose the last unsaved
terminal output; saved native history and session identity remain available.

Notifications continue while detached. Cleanup requiring confirmation waits until
attachment; background work never answers agent approvals or Git credential prompts.
On connection loss, detach and attach again. Protocol mismatches require stopping
the old daemon before starting the new version. Startup logs are stored under
`<data>/maestro/locks/<repository-key>.daemon.log`.

Use `maestro rename <slug> "New title"`, `prefix ,`, or “Rename task title” in the
palette. Titles accept up to 512 UTF-8 bytes without control characters. The slug
used by commands, branch, and worktree stay unchanged. See the
[Phase 5 acceptance checklist](docs/P5_UX.md) for platform and GitLab checks.

### Palette, diff, layouts, and themes

Use `prefix :` to search actions and tasks with fuzzy matching. Arrow keys select,
Enter runs the selection, and Escape returns to the agent. Actions retain their
usual confirmations. `prefix ?` opens scrollable help.
Clipboard paste and terminal bracketed paste both work in the palette. Agent
switching keeps the selected item visible in long lists; use PgUp/PgDn or the
mouse wheel to read long diagnostics and confirmation text.

`prefix D` opens a read-only diff of the task base against the current tracked
working tree, including committed, staged, and unstaged changes. Untracked paths
are listed separately and binary changes are identified. Use arrows/j/k to
scroll, left/right to pan, `r` to refresh, and Escape to close. Output is capped
at 2 MiB with an explicit truncation notice. External diff tools and textconv
filters are disabled in this view. `prefix d` still hides the current tab.
Horizontal scrolling reaches the end of every retained line, including long
minified files, and stops at the last useful column.

`prefix s` toggles a sidebar; below 72 columns it falls back to top tabs.
Drag a task onto another task to reorder in either layout. The order is saved
in SQLite and restored on the next launch. Your active agent stays selected.
Reopening an archived task appends it to that saved order.

Choose from four built-in themes:

| Theme | Command name | Colors |
|---|---|---|
| Forest | `dark` | Forest green, peach, and sage |
| Paper | `light` | Warm cream, ink, and terracotta |
| Catppuccin | `catppuccin` | Charcoal with pastel blue and lavender |
| Tokyo Night | `tokyo-night` | Deep navy, blue, and violet |

```sh
maestro theme                     # list themes and the current selection
maestro theme tokyo-night         # save globally
maestro theme light --local       # save for this repository
maestro theme auto                # follow the terminal background
```

Inside the TUI, use `prefix T` or search “Choose color theme” in the command
palette. ↑/↓ previews each theme immediately; Enter saves and Escape restores
the previous choice. The picker saves globally unless the repository already
sets its own `theme`, in which case it updates that override. CLI changes apply
on the next TUI launch. `--local` resolves linked worktrees to the main checkout.
Existing configuration comments and unrelated settings are preserved.

`auto` is the default selection mode rather than an additional palette.
Set `theme` to `auto`, `dark`, `light`, `catppuccin`, or `tokyo-night`. Auto asks
the terminal for its background color and defaults to dark if it gets no reply.
The default dark palette matches the website preview: a forest-green surface,
peach accent, and sage status colors. Light mode uses the site's cream and ink
palette. The shell has spaced tabs, an agent/status heading, inset panes, and
separate Git status and shortcut bars; short terminals use compact spacing.
Explicit Catppuccin and Tokyo Night palettes remain available.
Themes style Maestro's interface; agent programs retain their own ANSI colors.
Custom palettes require all eight colors:

```toml
theme = "my-theme"

[themes.my-theme]
background = "#20242c"
foreground = "#e5e9f0"
muted = "#adb8c9"
accent = "#8fbcff"
success = "#a3d9a5"
warning = "#f0ca80"
error = "#f49b9b"
merged = "#c9adf0"
```

Background Done/NeedsInput transitions show local alerts and briefly highlight
the task. Desktop delivery honors `[activity] notify_on = ["done", "needs_input"]`;
use `notify_on = []` to disable it. An unfocused terminal also receives alerts for
its selected task when focus reporting is supported. Linux uses `notify-send`,
macOS uses `osascript`, and Windows uses PowerShell and a temporary notification-area icon.
OS permissions, desktop services, and notification settings determine delivery.
Missing helpers leave the in-app alerts available and report availability once.

See [custom agent configuration](docs/CUSTOM_AGENTS.md) for complete template,
session identity, manual prompt, handoff, and troubleshooting guidance, and
[the P4 UX checklist](docs/P4_UX.md) for manual acceptance checks.

### GitHub and GitLab authentication

Run `maestro auth login`, or press `prefix g` inside Maestro, to start the
appropriate CLI's interactive login. Install `gh` for GitHub or `glab` for GitLab. `maestro auth status`
checks the credentials used by PR, CI, review, and merge commands.

Credential precedence is `GH_TOKEN`, `GITHUB_TOKEN`, `gh auth token`, then
`github.token` in configuration. Unset token environment variables before
interactive login; they override credentials stored by `gh`. After login,
retry `prefix P` to create or open a PR. Uppercase shortcuts require Shift;
`prefix p` pushes the branch.

For GitLab, token precedence is `GITLAB_TOKEN`, `GITLAB_ACCESS_TOKEN`, `OAUTH_TOKEN`,
`glab config get token --host <host>`, then `gitlab.token`. GitLab.com is detected
automatically. For a self-hosted instance, set:

```toml
[gitlab]
host = "gitlab.example.com"
# token = "..."  # Prefer glab's credential storage or environment variables.
```

`maestro auth login --host gitlab.example.com` also works outside a repository.
GitLab merge requests use the existing `pr`/merge workflow, including pipeline
and approval badges and expected-head protection. Squash and merge are supported;
GitLab project settings control the underlying merge strategy. `rebase` is rejected
rather than silently changing strategy. Unavailable approval data stays unknown.

Git pushes use Git's credentials separately. For HTTPS remotes, use
`gh auth setup-git` if Git still prompts for credentials; SSH remotes require
a working SSH key.

Inside Maestro, Git and SSH credential requests appear in a masked input.
Press Enter to submit or Esc (also Ctrl+C) to cancel the operation and close
the prompt. Passphrases retain spaces and symbols. Background cleanup never
opens a credential prompt; retry it manually if authentication is required.

### Archive and delete tabs

`maestro archive <task>` hides a task from the tab bar and default listings.
Inside the TUI, `prefix d` stops the current agent, saves its final history, and
archives the tab. Worktrees, branches, session identity, and saved history are
preserved. `maestro reopen <task>` makes the tab available in the attached TUI or on the next attachment. Stop a running agent with `maestro stop <task>` before CLI archive/cleanup.

Reopening a task with a retained PR restores its PR lifecycle so polling can
detect changes made while it was archived. After cleanup of a completed task,
reopening creates a fresh branch for the next cycle. This allows normal pushes
after a squash merge even when the Git host retains the previous source branch.

Recreated worktrees receive the configured `worktree.copy` files and
`worktree.setup` commands before the task becomes active. Failed provisioning
keeps the task archived; retrying reopen resumes at the unfinished step.
Cleanup preserves commit snapshots under `refs/maestro/recovery/<task-id>/<commit>`
across successive task cycles, including local commits saved during force cleanup.

`maestro tabs` (also `ls` or `list`) lists unarchived tasks. Use `--all` to include
archived tasks, `--archived` to show only archived tasks, and `--json` for scripts.
These listings remain available while the TUI is running.

`maestro rm <task>` (also `delete`) permanently deletes an archived task and all
its saved Maestro history. It refuses unarchived tasks. The Git worktree and
branch remain on disk; this command does not clean them up or delete the agent's
own logs. Retained branches/worktrees must be dealt with before reusing the same
task slug. Quit the TUI before using CLI archive, reopen, or delete commands,
since the TUI holds the project lock.

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

Data lives in the platform's XDG data directory under `maestro`: `maestro.db`, `locks/`, and `worktrees/<repository-key>/<task-slug>/`. The repository key hashes the Git common directory, keeping checkouts with identical names separate. Commands run from a linked worktree resolve to the same project as the main checkout. The daemon holds the project lock for its lifetime and allows one attached TUI. CLI mutations route through the daemon; listing and history remain available while detached. A second attached TUI reports that the project is busy.

`worktree.copy` copies regular files only, skips missing files, and rejects symlinks, traversal, Git metadata and existing destinations. Copied files have owner-only permissions. `worktree.setup` runs trusted shell commands from your config in each new worktree (`sh` on Unix, `cmd` on Windows). Review repository config before using it. If initial copying, setup or database persistence fails after worktree creation, Maestro retains the checkout and reports its path and branch. Inspect and recover it manually before retrying creation; failed initial provisioning is not listed as a saved task. Reopen provisioning retains the archived task and saves progress for retry.

### Continue a task on another machine

Publish a checkpoint with the task branch to carry its goal, notes, preferred
agent, archive status, and bounded conversation context through Git:

```sh
maestro checkpoint fix-login
# In the task checkout, review the checkpoint and code before publishing:
git add .maestro/.gitignore .maestro/tasks
git commit -m "chore: checkpoint task context"
git push -u origin HEAD
```

Commit your code changes as well. In the TUI, `prefix C` (or “Stop agent and save
portable checkpoint” in the palette) stops the agent, saves its final history,
and writes the checkpoint. Use `prefix r` to resume locally. The CLI checkpoint
command requires the TUI to be closed. Checkpointing never stages, commits, or
pushes files automatically. Once a task has a checkpoint, Maestro's push/PR
commands reject stale or uncommitted context; checkpoint and commit again after
changing notes or running another session.

On another machine, install Maestro and your preferred agent, authenticate the
agent, then clone the repository or run `git fetch origin` in an existing clone.
Run `maestro ls` to discover tasks and `maestro open fix-login` to continue.
Fetched task branches are sufficient: Maestro imports their committed metadata
and creates local worktrees when launching them. If the task branch is already
checked out, that checkout is reused, including the main checkout. Existing
checkouts are never reset or silently pulled; update a checkout with Git when
its checkpoint differs from the selected one. Discovery does not run worktree
setup scripts or copy private files; prepare dependencies and local
configuration as needed.

Checkpoints live in `.maestro/tasks/<uuid>/task.json` and `handoff.md` at the
root of the task branch's checkout. Only branches that carry their own task
manifest are discovered; merged copies on unrelated branches are ignored.
Fetching one task branch recovers that task; fetch all relevant branches to
recover all published tasks. Remote branches must remain available for recovery.

SQLite, locks, native agent sessions, credentials, and full raw transcripts stay
local. Continuation on a new machine uses a **fresh agent session with the saved
handoff**, not the old agent's native session. Uncommitted/unpushed work and
history after the last published checkpoint are not transferred. Review the
handoff before committing: conversation and terminal excerpts can contain
private information. The handoff uses the configured token budget and can omit
older context; goal and notes are also retained separately in the manifest.

Repeated discovery is idempotent. Local edits or conflicting checkpoints produce
an actionable error instead of overwriting context. Use
`maestro restore origin/fix-login --replace` to explicitly choose a fetched
branch's checkpoint. Maestro remembers that choice when branches diverge and
preserves local history and Git files. A local checkout with the same committed
checkpoint can continue on its own code; a different checkpoint requires Git
reconciliation before launch. `maestro checkpoint` remains available during
discovery conflicts so you can preserve local context first. After renaming a
task's branch with Git in its existing checkout, run
`maestro checkpoint fix-login --branch new-name` to
adopt the name and update its manifest. Maestro does not guess ownership of
inherited manifests.

Archive/reopen status travels only after a new checkpoint is committed and
pushed. To clean up a portable task, archive it, checkpoint the archived status,
commit and push using Git, then run `maestro archive fix-login --cleanup`.
Maestro refuses cleanup of unpublished context. The main checkout cannot be
removed by task cleanup. `maestro rm` suppresses automatic rediscovery of that
task in the current local database; explicit `maestro restore <ref>` can recover
it again. Removing a remote branch never deletes local task records.

Existing tasks become portable on their first checkpoint. Maestro migrates its
own legacy Git exclusion while retaining unrelated ignore rules. Files such as
legacy `.maestro/session-id` remain local. If your own `.gitignore` excludes all
of `.maestro/`, adjust that rule to permit `.maestro/tasks/` and `.maestro/.gitignore`.

### Context handoff and history

Use `prefix a` to choose an installed agent; the picker marks agents that previously
worked on the task. Switching away from an active or approval-waiting agent asks
before interruption. Maestro stops the old process, saves its final transcript,
and writes `.maestro/local/handoff.md` in the same worktree before launching the new
agent. Returning to an agent resumes that agent's own native session ID.

`maestro switch <task> -a <agent>` opens the focused TUI, restores other task tabs,
and performs the same handoff. It needs an interactive terminal. Only one TUI can attach to the daemon at a time, so use its switch dialog when a
TUI is already attached.

Handoffs include the goal, notes, agent timeline, explicit TODO mentions, recent
conversation and Git state. Set `[handoff] token_budget = 6000` to adjust the
budget. The deterministic estimator conservatively counts each UTF-8 byte as a
token; actual model token usage is usually lower. Older dialogue and tool output
are trimmed, with omission markers. Terminal snapshots retain their newest lines
and remain in the handoff when native history could not be fully imported.
No model calls are made to summarize history.

Use `prefix n` to edit multiline task notes. Ctrl+S saves them and Escape cancels;
notes are kept in SQLite and included in subsequent handoffs. Outside the TUI,
`maestro notes <task>` prints them, `maestro notes <task> --set "text"` replaces
them, and `--file notes.md` or `--file -` reads replacement notes from a file or
stdin. `--set ""` clears notes. Notes accept up to 64 KiB of UTF-8 text. Reading
works while the TUI is open; updates use the project lock.

Switching validates that the selected new/resume template includes the full
handoff instruction in its rendered arguments before stopping the current agent.
If an agent cannot accept prompts on launch, configure `manual_prompt = true`.
Manual-prompt agents start without injected input; use `prefix H` to copy the
instruction, then paste it into their pane.

A pending handoff is stored in SQLite before launch. If writing the file or
starting the new process fails, retry with `prefix r`; the saved handoff is reused.
An early startup crash also retains it. Delivery is acknowledged by a new native
turn signal, a clean process exit, or an active pane surviving a two-second startup
window (only for generic agents without native turn adapters). Native adapters
retain pending context until a new turn signal or clean exit. If startup never
created the recorded native session, use `prefix R` to retry fresh with the saved
handoff. Merely creating a process no longer consumes pending context.
If the selected target has no discoverable resume identity, the switch dialog
asks before stopping the current agent and starting the target fresh with a
handoff. This also applies to `maestro switch`. Use `prefix R` to start the
current agent fresh. Use `prefix &` for worktree and branch cleanup.

`prefix h` opens history: Tab selects a session, Enter expands it, `t` reveals tool
details, arrows/j/k and page keys scroll, `r` refreshes, and Escape returns to the
pane. Terminal fallback snapshots are labeled `terminal`. `maestro history <task>
--json` returns `task`, `sessions`, `turns`, `events` and `handoffs`. Native records
are deduplicated across resumed launches. Persisted history remains available
from the main repository even after the worktree or agent's source logs disappear.
Terminal fallback capture preserves the alternate screen before teardown and
keeps older scrollback lines at their original width after a terminal resize.

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

The test suite includes teatest/v2 screen coverage for every P4 view and existing dialog, persisted drag ordering, bounded diff loading, notification filtering, and real PTY processes, three-task restoration, a controlled Codex → agy → Codex handoff, pending-switch recovery, P1 database migration, duplicate-free transcript imports, native activity precedence, retained history after worktree deletion, and UI snapshots at 80×24 and 160×48. Regenerate snapshots with `go test ./internal/tui -update` and review the diff. A fake agent is available with `go build -o /tmp/maestro-fakeagent ./internal/testutil/fakeagent`; configure `new = ["new", "{{.SessionID}}"]`, `resume = ["resume", "{{.SessionID}}"]` and `generate_session_id = true` to try it without API credentials.

Linux terminal smoke tests and Windows/macOS cross-builds have been performed. Read-only parser checks also exercise existing local Codex/agy transcripts and OpenCode native history. Native Windows/macOS behavior and authenticated live cross-agent conversations remain manual checks. GitHub PR, CI, review, merge, cleanup, and reopen behavior is covered by fake-server and integration tests; a real authenticated GitHub run remains a manual acceptance check.

### Layout

| Path | Purpose |
|---|---|
| `cmd/maestro` | Binary entry point |
| `internal/cli` | Cobra command tree |
| `internal/version` | Build metadata (set via `-ldflags`) |
| `internal/{app,config,store,core,git,agent,term,tui}` | P1: MVP |
| `internal/handoff` | P2: context handoff between agents |
| `internal/forge` | GitHub PRs / GitLab MRs, CI, merges |
| `cmd/maestrod`, `internal/daemon` | Background runtime, local IPC, detach/attach |

## License

[MIT](LICENSE)

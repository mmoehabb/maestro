# Configure a custom agent

Maestro launches any interactive CLI configured under `[agents.<name>]`. No Go
adapter is required. Install and authenticate the agent first, then add its
configuration to your global `maestro config path` file or the repository's
`.maestro.toml`. Repository tables override individual global/default fields.
Run `maestro doctor` to check executable discovery.

## Commands and arguments

This example assumes your CLI supports the illustrated flags; replace them with
its actual interactive new/resume commands:

```toml
[agents.my-agent]
cmd = "my-agent"
new = ["--session-id", "{{.SessionID}}", "{{.Prompt}}"]
resume = ["--resume", "{{.SessionID}}", "{{.Prompt}}"]
generate_session_id = true
input_hints = ["Approve?", "Allow command?"]
```

Start with `maestro new "Fix login" -a my-agent`, then open `maestro`. The agent
runs in that task's worktree. Each array entry is one argument; empty rendered
arguments are omitted. Maestro does not invoke a shell, split quoted strings,
expand `$VARIABLES`, or interpret pipes. Put executable flags in `new`/`resume`,
not in `cmd`. An absolute executable path is supported.

Templates receive `.Dir` (worktree), `.SessionID`, `.Prompt`, `.Env`, and
`.NewSession`. The child also receives `MAESTRO_TASK` and `MAESTRO_SESSION_ID`.
For an optional flag and value, use separate arguments:

```toml
new = ["{{if .Prompt}}--prompt{{end}}", "{{.Prompt}}"]
```

## Choose a session identity strategy

A resumable agent needs one of these mechanisms:

| Setting | Use when |
|---|---|
| `generate_session_id = true` | The CLI accepts a Maestro-generated UUID for a new session. |
| `session_file = ".maestro/session-id"` | The CLI or a wrapper writes its native session ID into a worktree-relative file. |
| `session_create = ["create-chat"]` | A separate command allocates a session and writes one ID to stdout. |
| Built-in agent preset | Maestro already knows how to discover that agent's native identity. |

`session_create` is literal argv passed to `cmd` before the first launch. It
requires `resume` and cannot be combined with either other identity setting.
Generated IDs also require `resume`. Use a safe relative `session_file` path and
ensure it belongs to the current task's launch. On resume, Maestro passes the
saved native ID and does not replay the original prompt.

For a stateless agent, omit `resume` and identity settings. Restarting starts a
new process. For a resumable agent with missing identity, Maestro asks for an
explicit fresh start (`prefix R`) rather than silently abandoning its session.

## Handoffs and manual prompts

Switching agents writes `.maestro/local/handoff.md` with the goal, notes, conversation,
and Git state. Both `new` and `resume` templates should pass `{{.Prompt}}` so
Maestro can deliver the handoff instruction. It validates the rendered arguments
before stopping the current agent.

If a CLI cannot accept a prompt while remaining interactive, set:

```toml
manual_prompt = true
```

Leave the initial prompt blank. Use `prefix H` to copy the handoff instruction
and paste it into the agent pane. Maestro never injects simulated keystrokes to
bypass this requirement. Pending handoffs survive startup failures.

## History and activity limits

Custom agents use saved terminal history and the quiet timer; they do not gain
native transcript parsing from TOML configuration. Terminal notifications can
also mark a turn complete, while `input_hints` recognize approval/question text.
These signals are heuristics: a quiet tool can still be running. Codex, agy, and
OpenCode have native adapters with stronger turn signals and structured history.

Use `[activity] idle_after = "2s"` to configure the quiet period. Desktop alerts
for background tasks are controlled by `notify_on = ["done", "needs_input"]`;
set `notify_on = []` to disable desktop delivery.

## Troubleshooting

- **Executable not found:** run `maestro doctor`; check `cmd` and your PATH.
- **Unknown flags/noninteractive exit:** adjust templates to your installed CLI's
  interactive mode. Test the command directly in a terminal.
- **Cannot deliver the prompt:** include `{{.Prompt}}` in the selected template,
  or use `manual_prompt` when the CLI cannot accept it interactively.
- **Session cannot resume:** check the identity source and `resume` template.
  Use `prefix R` only when you intend a fresh session.
- **Premature Done:** adjust the quiet timer; generic activity is heuristic.

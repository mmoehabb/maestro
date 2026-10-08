# P4 interface acceptance

Automated coverage exercises real Bubble Tea message routing with teatest/v2,
plus deterministic screen goldens at 80×24 and 160×48. Native desktop delivery
and platform terminal behavior require the manual checks below.

## Verification performed

- Race-enabled automated tests, vet, and the pinned linter.
- Linux amd64, Windows amd64, and macOS arm64 builds.
- An isolated Linux PTY smoke run with three fake agents: palette selection,
  diff, sidebar, drag reordering, saved order, resize, prompt forwarding,
  scrollable help, graceful quit, and resume with the same native session IDs.

Native macOS/Windows terminal execution and real desktop delivery on all three
platforms remain manual acceptance checks. The smoke run disables desktop
notifications and uses temporary repositories and data directories.

## Review checklist

1. Open three tasks. Search with `prefix :`, select a task, and invoke an action.
   Disabled actions explain why; Escape restores input to the agent. Help is
   scrollable and displays the same bindings as the palette.
2. Open `prefix D`. Confirm committed, staged, and unstaged tracked changes are
   shown against the named base. Check untracked/binary files, an empty diff,
   missing worktree, horizontal scrolling, and refresh during active edits.
3. Toggle `prefix s`. Resize through narrow and wide terminal sizes. Verify
   agent cursor position, mouse clicks, wheel routing, scrollback, and paste.
   Drag in both layouts, quit/reopen, and confirm order and session identity.
4. Review auto dark/light, explicit themes, and a custom palette. Check runtime,
   PR, CI, review, and selection indicators; confirm agent ANSI colors survive.
5. Finish a turn in a background task, then trigger an approval question. Expect
   one toast/attention highlight and one enabled desktop alert per transition.
   Repeat with the terminal unfocused and with `notify_on = []`. Disable the
   OS notification helper and confirm the TUI remains usable with local alerts.
6. Inspect every dialog at both golden sizes, including long names, errors,
   empty lists, and task-list overflow. Check Enter, Ctrl+C, prefix fallback,
   and mouse input still reach the agent outside Maestro views.

Repeat terminal and notification checks on Linux, macOS, and Windows. A
cross-build confirms compilation only; it does not establish native delivery.

Regenerate and review snapshots with:

```sh
go test ./internal/tui -update
git diff -- internal/tui/testdata
```

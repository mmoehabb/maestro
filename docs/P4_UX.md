# P4 interface acceptance

Automated coverage exercises real Bubble Tea message routing with teatest/v2,
plus deterministic screen goldens at 80×24 and 160×48. Native desktop delivery
and platform terminal behavior require the manual checks below.

## Review corrections

The [P4 review plan](P4_REVIEW_PLAN.md) identified five functional issues. The
implementation now routes scoped clipboard/component replies, rejects older
activity revisions, persists appended reopen positions, scrolls switch dialogs
and wraps their confirmations, and bounds diff panning by retained line widths.

Regression coverage includes controlled clipboard helpers (no access to the
user's clipboard), delayed-event/snapshot permutations, rapid turns and pane
replacement, concurrent reorder/reopen, native-session restoration after
archive/reorder/reopen, long diff lines with tabs/Unicode, and switch overflow,
errors, confirmations and resize in both layouts. Added golden fixtures cover
fresh-start confirmation and overflowing switch lists at both required sizes.
Color-cell assertions check explicit agent foreground/background preservation
and theme accents beyond ANSI-stripped screen snapshots.

Review-fix verification on 2026-10-08: the full race-enabled test suite passed,
with a subsequent passing TUI/store race run after the final cleanup changes.
Vet, golangci-lint v2.9.0, and module tidiness passed; Linux amd64, Windows amd64,
and macOS arm64 builds succeeded. The affected and added snapshots were reviewed.
These automated checks do not complete the native platform acceptance below.

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
   Check Ctrl+V and bracketed paste, including closing/reopening the palette
   before a clipboard reply arrives.
2. Open `prefix D`. Confirm committed, staged, and unstaged tracked changes are
   shown against the named base. Check untracked/binary files, an empty diff,
   missing worktree, horizontal scrolling, and refresh during active edits.
   Pan through a line longer than 4096 columns and resize at its right edge.
3. Toggle `prefix s`. Resize through narrow and wide terminal sizes. Verify
   agent cursor position, mouse clicks, wheel routing, scrollback, and paste.
   Drag in both layouts, quit/reopen, and confirm order and session identity.
   Archive a task, reorder the remaining tasks, reopen the archived task, and
   confirm the appended order is identical after another restart.
4. Review auto dark/light, explicit themes, and a custom palette. Check runtime,
   PR, CI, review, and selection indicators; confirm agent ANSI colors survive.
5. Finish a turn in a background task, then trigger an approval question. Expect
   one toast/attention highlight and one enabled desktop alert per transition.
   Repeat with the terminal unfocused and with `notify_on = []`. Disable the
   OS notification helper and confirm the TUI remains usable with local alerts.
6. Inspect every dialog at both golden sizes, including long names, errors,
   empty lists, and task-list overflow. Check Enter, Ctrl+C, prefix fallback,
   and mouse input still reach the agent outside Maestro views.
   Include enough custom agents to overflow the switch list and test the
   fresh-session confirmation, long errors, PgUp/PgDn, and cancel behavior.

Repeat terminal and notification checks on Linux, macOS, and Windows. A
cross-build confirms compilation only; it does not establish native delivery.

Regenerate and review snapshots with:

```sh
go test ./internal/tui -update
git diff -- internal/tui/testdata
```

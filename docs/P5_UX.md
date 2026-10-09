# Phase 5 acceptance checks

## Detach and attach

1. Create three tasks and open Maestro. Record native session IDs and agent PIDs.
2. Start a long turn, press `prefix q`, and check `maestro daemon status`:
   no TUI attached, agents still running. Closing the terminal should behave the same.
3. Run `maestro attach`. Verify the same PIDs and native IDs, output written while
   detached, alternate-screen rendering, scrollback, cursor, mouse, paste, and resize.
4. Try a second TUI. It must report the existing attachment without interrupting it.
5. While attached and detached, use CLI notes, rename, create, and reorder from the
   TUI. Verify persisted changes, stable task identity, and live title updates.
6. Copy scrollback immediately after reconnect. The copied text should contain
   current output, including output written while detached.
7. Trigger Git authentication. Confirm that the attached TUI shows a masked
   prompt, preserves the entered text, and cancels cleanly. Background operations
   needing credentials should fail with instructions to retry interactively.
8. Merge a request while detached. Confirm that cleanup follows `ask`/`auto`/`never`;
   an `ask` confirmation must wait for the next attachment. Dirty and unpushed
   work must retain the existing protections.
9. Run `maestro stop <task>`, then `maestro daemon stop`. Verify agent descendants
   exit, final history is saved, and reopening resumes saved native IDs.
10. Terminate a test daemon abruptly. Reopen Maestro and verify stale endpoint
    recovery, existing saved history, and native-session resume. Unsaved final
    terminal output can be lost in a daemon crash. Test a protocol mismatch and
    verify that it gives an actionable upgrade error.

Repeat on Linux, macOS, and Windows Terminal. Cross-compilation alone does not
validate ConPTY lifetime, named-pipe ACLs, desktop alerts, or terminal behavior.

## GitLab

Use a disposable GitLab.com project, then a configured self-hosted project with
nested namespaces. Test HTTPS and SSH remote parsing, `glab` authentication,
explicitly empty MR descriptions, pipeline/review badges, expected-head rejection
when another commit is pushed, squash/merge, cleanup, archive, and reopen.
An unavailable approvals API must leave review status unknown rather than approved.
GitLab's project settings control merge strategy; Maestro rejects `rebase` as an
unsupported per-request method. Test API permission errors and rate-limit backoff.

## Titles

Rename through `maestro rename <slug> <title>`, `prefix ,`, and the palette.
Verify Unicode, length/control-character validation, cancel/save behavior, visible
labels, restart persistence, and unchanged slug, branch, worktree, notes, and history.

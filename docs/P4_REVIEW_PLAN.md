# P4 review and remediation plan

Reviewed 2026-10-08 at `17dc638` (P4 implementation: `eb24496`). The findings below describe that reviewed baseline. The user subsequently authorized implementation; R1–R5 have now been addressed in the working tree with regression tests. Native platform acceptance remains pending.

## Implementation status

| Finding | Resolution |
|---|---|
| R1 | Component commands carry their input owner; palette/new-task replies update only that live, focused input. Controlled clipboard and stale-reply tests added. |
| R2 | Pane activity has monotonic revisions shared by snapshots/events; the TUI rejects old observations and resets state on pane replacement. Ordering, rapid-turn and failure tests added. |
| R3 | Reopen allocates its append position atomically with the lifecycle transition and returns the committed position. Restart/concurrency tests and native-session restoration coverage added. |
| R4 | Switch lists follow selection; confirmation text wraps in a dedicated view; long content scrolls with reserved action instructions. Resize, sidebar and error tests plus fresh/overflow goldens added. |
| R5 | Panning uses retained content's display width, including expanded tabs and Unicode, and clamps after resize. Long-line reachability tests added. |

The specification now documents the delivered P4 layouts, highlights and toast
coverage. Title-only rename is explicitly deferred to a P5 UI follow-up. Theme
cell tests verify explicit agent colors survive shell composition.

Verification after implementation (2026-10-08): `go test -race -count=1 ./...`
passed, followed by a passing TUI/store race run after the final observation
cleanup and test additions. `go vet ./...`, golangci-lint v2.9.0 (zero issues),
and `go mod tidy -diff` passed. Linux amd64, Windows amd64, and macOS arm64
binaries built successfully into temporary output paths. Confirmation golden
changes and the new fresh-start/overflow fixtures were reviewed. Native desktop
delivery and macOS/Windows terminal execution were not performed.

At the reviewed baseline, P4 implemented all seven headline deliverables but was not ready for an unconditional UX acceptance sign-off. Five functional issues were reproduced, and native platform acceptance remains incomplete. The architecture is generally sound: UI operations call the service/runtime, diff loading is asynchronous and bounded, stale diff requests are rejected, order writes are transactional, theme updates preserve unrelated configuration, and notification content is passed as data rather than executable script text.

## Scope and evidence

Reviewed the P4 commit against `docs/PLAN.md`, `docs/P4_UX.md`, README behavior, and custom-agent documentation. Traced the action registry, input routing, diff execution/rendering, layout/cursor/mouse geometry, persistence, theme selection/configuration writes, notification delivery, and their interactions with existing lifecycle flows.

- `go test -race ./...` passed across all packages, including real PTY tests and the existing screen snapshots.
- Six temporary regression probes reproduced the five issues below. They used Go's `-overlay` mechanism; test source and controlled clipboard helpers stayed under `/tmp/maestro-p4-review`, outside the repository. These probes deliberately assert the desired behavior and fail against the current implementation.
- Native macOS/Windows terminal execution and actual desktop notification delivery were not performed. Existing documentation explicitly leaves these as manual acceptance items. Cross-compilation cannot establish those behaviors.
- The initial review changed no repository source, configuration, existing tests, or golden files. The subsequent authorized implementation is summarized above.

Severity below uses **P2 = functional defect to fix before P4 acceptance**, and **P3 = lower-impact usability limitation**. These priorities are separate from roadmap phase names.

## Confirmed findings

### R1 — P2: Clipboard paste into the command palette is dropped

**Location:** `internal/tui/palette.go:124`; `internal/tui/model.go:524`.

The text input handles Ctrl+V by returning a command whose result is Bubbles' private `textinput.pasteMsg`. The model routes keyboard events and terminal `tea.PasteMsg` to the palette, but its fallback only updates PR and notes inputs. The clipboard result never reaches the palette. Cursor blink messages are also omitted from that route.

**Reproduction:** Open the palette, press Ctrl+V with `clipboard-query` in the clipboard, execute the returned command, and deliver its result through `Model.Update`. The result is `textinput.pasteMsg("clipboard-query")`, but the palette value remains empty. Terminal bracketed paste is a separate path and does work.

**Planned fix:** Add a general palette input-update path for component messages. Preserve the existing action-key interception; forward remaining input messages and returned commands to the active input, and reset selection whenever its value changes. Audit the existing new-task inputs for the same omitted component-message path. Ensure a reply from a closed palette cannot affect an unrelated view or agent.

**Acceptance tests:** Real Ctrl+V command/result routing with a fake clipboard helper; bracketed paste; selection reset after paste; Escape before a clipboard reply arrives; cursor messages; no input leaking to the agent while the palette is open.

### R2 — P2: Snapshot/event interleaving duplicates activity notifications

**Location:** `internal/tui/model.go:329`, `internal/tui/model.go:369`, `internal/tui/notifications.go:23`.

Both pane snapshots and runtime events update the same state. Deduplication only compares the previous state string. A tick can observe Done before an older queued Working event is consumed. That older event moves the observed state backward, so the later Done event sends another toast and desktop notification for the same turn. Checking pane identity does not reject delayed events from the current pane.

**Reproduction:** With a real PTY pane, establish Working, complete one native turn, process a tick, then deliver the same pane's queued Working and Done events. The recorded notifier receives two `add-tui: done` messages for that one turn. This is a logical ordering bug, not a memory race; the race detector does not catch it.

**Planned fix:** Give activity observations a per-pane monotonic transition revision shared by snapshots and events. Track the latest accepted revision and the last notified transition; reject older observations and reset tracking when the pane changes. Retain snapshot reconciliation for dropped events. Do not solve this with a time debounce that suppresses legitimate rapid turns.

**Acceptance tests:** Snapshot-before-event and event-before-snapshot permutations; delayed events from both current and replaced panes; repeated Done/NeedsInput observations; two genuine rapid turns; focused/background/blurred states; disabled delivery; helper failure. Each eligible transition must produce exactly one notification.

### R3 — P2: Reopening an archived task breaks persisted visible order

**Location:** `internal/store/order.go:29`; `internal/core/cleanup.go:173`; `internal/tui/forge.go:317`.

Reordering assigns dense positions to visible tasks while archived tasks retain their previous positions. Reopening does not allocate a new position, yet the TUI appends the reopened task. Database restoration sorts by `tab_order, id`, so the order displayed before quitting can differ from the order after restarting.

**Reproduction:** Create A, B, C; archive A; reorder the remaining tasks to C, B; reopen A. The TUI appends it as C, B, A, but persisted listing restores A, C, B because A and C both have order zero. The probe exercised the same `SaveWorkflow` persistence path used by reopen.

**Planned fix:** Define reopen placement as append, matching current TUI behavior. Allocate the reopened task's visible position atomically when the successful reopen transition commits, and return that committed position. Keep order ownership in the store/service rather than issuing independent UI writes. Ensure concurrent reorder/reopen either commits a consistent order or rejects a stale request without partial updates.

**Acceptance tests:** Archive → reorder → reopen → close/reopen database; identical TUI and stored ID sequences; repeated archive/reopen cycles; task creation during reorder; failed/retried reopen; stale reorder rejection; preserved active task, pane, and native session identity.

### R4 — P2: Switch dialogs hide selected agents and confirmation instructions

**Location:** `internal/tui/context.go:138`; `internal/tui/model.go:654`; shared pane sizing in `internal/tui/chrome.go`.

The switch view renders every agent and an unwrapped confirmation string, then the shell truncates rows and columns. There is no viewport that follows the selection. At 80×24, selecting the twentieth configured agent leaves the selected row entirely invisible. Separately, even a one-agent fresh-start confirmation is cut after “start codex fresh”, hiding “with a handoff? y / n”. The reduced P4 pane area exposes an existing dialog limitation; this is a P4 UX acceptance gap rather than a wholly new dialog implementation.

**Reproduction:** Two independent probes showed the invisible final selection with twenty agents, and the clipped fresh-start prompt at the required 80×24 size.

**Planned fix:** Pass available width and height into the switch dialog. Window the agent list around the selection; wrap confirmation/error text; reserve space for action instructions. Give the confirmation state its own layout if the list and prompt cannot fit together. Audit other dialogs at the actual sidebar pane width, and use scrolling or a compact layout where required rather than relying on final output truncation.

**Acceptance tests:** Default agents plus enough custom agents to overflow; last-item selection and wraparound; interrupt and fresh-session confirmations; long names and errors; tab/sidebar layouts; 80×24 and 160×48; resizing while a dialog is open. Assert that selected content and the confirmation choices are visible, not merely that output fits the terminal. Preserve Escape/cancel and require the intended confirmation before switching.

### R5 — P3: Long diff lines cannot be fully inspected

**Location:** `internal/tui/diff.go:72`.

Horizontal movement is capped at column 4096, independently of retained line lengths. A patch below the byte limit can contain a much longer line, such as minified JavaScript or a JSON fixture. Its tail becomes unreachable without any truncation indication. Short patches also allow scrolling far past all content into an empty view.

**Reproduction:** Render a 5,000-character added line ending in `IMPORTANT_END`, then repeatedly pan right. The horizontal offset stops at 4096; the end marker is never visible at a 76-column pane width.

**Planned fix:** Derive the horizontal bound from the display width of retained content and the current viewport. Clamp movement to the final useful column, recalculating on refresh/resize. Preserve the byte bound on Git output. If a separate column limit is intentionally retained, expose that truncation explicitly and provide a way to inspect the omitted content.

**Acceptance tests:** End-of-line reachability for long lines; short-line overscroll; tabs and wide Unicode; resize at the right edge; loading/error/refresh states; existing byte-truncation notice and stale-request rejection.

## Coverage and incomplete acceptance

| P4 deliverable | Assessment | Follow-up |
|---|---|---|
| Command palette | Shared action registry, fuzzy task/action matching, disabled-state handling, navigation implemented | R1; exercise actual component command replies |
| Diff view | Base-to-working-tree patch, tracked/untracked/binary handling, timeout/output bounds, refresh and scrolling implemented | R5; extend long-content tests |
| Sidebar layout | Shared sizing, cursor translation, narrow fallback and mouse routing implemented | R4; test dialogs and drag behavior with overflowing lists |
| Themes | Four built-ins, auto detection, custom TOML palettes, preview/cancel/save and CLI persistence implemented | Validate colors and agent ANSI preservation beyond text snapshots; native terminal checks |
| Mouse drag/order | Transactional reordering, active-task preservation and direct restart tests implemented | R3; cover archive/reopen interactions |
| Desktop notifications | Platform helpers, focus/config filtering and local fallback implemented | R2; actual OS delivery and failure acceptance |
| Custom-agent documentation | Launch arguments, session identity, handoff/manual prompts and heuristic limits documented | R4; exercise a sufficiently large custom-agent catalog |

The screen suite has useful breadth but does not establish every acceptance claim. `TestP4Screens` constructs prepopulated models, sends a finish message, and compares ANSI-stripped final views. The switch fixtures contain only two agents; there is no fresh-session confirmation fixture. These snapshots cannot catch clipboard command routing, delayed-event notification duplication, or color regressions. Preserve them and add interaction assertions and relevant color/cell checks rather than just regenerating expected output.

The original specification also needs reconciliation before calling the whole TUI plan complete:

- `docs/PLAN.md:407` still promises `prefix ,` to rename a task, but no rename action or service operation exists. This is an inherited, unassigned roadmap gap rather than a demonstrated P4 regression. Either explicitly defer it with a phase and remove the misleading binding, or implement title-only rename with a persisted service operation, palette/help registration, validation, cancel/error handling, and a restart test. Keep slug/branch/worktree renaming a separate scope.
- The original UI description promises centered dialogs with dimmed backdrops, a flashing/pulsing attention indicator, and bottom-right toasts for CI failure and agent exit. The delivered interface generally replaces pane content, uses a temporary static highlight, and puts toast text in the footer; activity alerts cover Done/NeedsInput. Record the accepted design changes and event coverage in the specification, or schedule the omitted behaviors. The later P4 implementation summary already describes some of these narrower choices; they should not remain contradictory promises.
- The picker offers four built-ins, while auto/custom selection is available through CLI/configuration. Current documentation describes that split; it is not counted as a defect. Add those picker options only if choosing every supported theme inside the TUI is an acceptance requirement.

## Proposed implementation sequence

1. **Fix input routing and dialog visibility (R1, R4).** Add the failing interaction cases first; implement message forwarding and viewport-aware switch/confirmation rendering; review updated snapshots at both required sizes and in the sidebar.
2. **Make activity notifications ordered (R2).** Introduce transition identity across the pane/runtime/UI boundary and test all delivery permutations without real desktop helpers.
3. **Make order durable across lifecycle changes (R3).** Commit reopen placement in the store transaction; verify the complete archive/reorder/reopen/restart flow through the runtime and TUI.
4. **Complete long-line diff navigation (R5).** Use content-derived display bounds and verify retained text stays reachable after resizing and refreshing.
5. **Close validation and specification gaps.** Reconcile the inherited feature promises above, then run the acceptance matrix below. Keep implementation delivery and platform acceptance as separate status statements until both are complete.

Each item should be an independently reviewable change with the behavioral regression tests described above. Existing safety checks for worktree cleanup, explicit fresh starts, and native session identity must remain intact.

## Completion criteria

- All five findings have regression coverage and pass; no implementation fix is replaced solely by a golden update.
- `go test -race -count=1 ./...` passes after the fixes; run the repository's vet, pinned lint, build, and CI matrix checks before merge.
- Review required-size snapshots with long names, long errors, empty lists, overflowing custom agents/tasks, fresh-start confirmation, both layouts, and resize transitions. Check actual key/clipboard/paste/mouse routing through Bubble Tea.
- Check semantic colors and explicit agent ANSI foreground/background preservation, including preview/cancel and automatic background selection. ANSI-stripped goldens alone are insufficient.
- On Linux, macOS, and Windows, record terminal/OS versions and results for keyboard negotiation, Enter/Ctrl+C, paste, pane resize/cursor/mouse behavior, drag order, quit/resume, and session identity.
- On each OS, verify one real desktop alert per eligible Done/NeedsInput transition, focused and unfocused behavior, `notify_on = []`, and missing/denied helper behavior with usable in-app fallback.
- Update `docs/P4_UX.md` with performed checks and remaining limitations. Mark native acceptance complete only when actually executed.

# Native parser fixtures (version 1)

These are synthetic records, containing no copied private conversation content.
Record shapes were inspected locally on 2026-10-05: Codex rollout JSONL, agy
`.system_generated/logs/transcript.jsonl`, and OpenCode message/part records.
OpenCode's documented `export <sessionID>` command supplies the info/messages
wrapper. Fixtures cover user/assistant text, tools, streaming updates and final
turn signals. Each parser ignores reasoning and internal instructions.

agy `DONE` describes a step, not necessarily a turn. Only a completed textual
planner response with no tool calls is treated as final. Unknown format versions
use terminal fallback; these private formats are not guaranteed stable APIs.

Local OpenCode 1.18.34 returned empty stdout for session listing/export on this
machine. A read-only fallback reconstructs the same messages/parts from its
observed SQLite schema. The fallback test builds a synthetic database from these
fixtures and verifies workspace matching, child-session exclusion, cancellation,
and write refusal; missing databases are never created.

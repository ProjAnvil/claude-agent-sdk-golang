# Plan: Port Python SDK v0.2.88-v0.2.110 to Go SDK

## Summary

Port all functional changes from Python SDK v0.2.88 through v0.2.110 to the Go SDK.
After filtering, the range contains exactly **one** functional change requiring a Go
port: the `TaskUpdatedMessage` typed lifecycle message (Python v0.2.101, PR #1016).
All other commits in the range are CLI version bumps, changelog/release housekeeping,
a Python-specific `anyio`/`trio` async-backend port, and a Python dependency pin —
none of which apply to Go.

A defensive content-block validation fix (Python PR #1058, commit `d47b180`) exists on
Python's `origin/main` but is **post-v0.2.110** (unreleased) and is explicitly
**out of scope** for this plan per the agreed boundary (strict v0.2.110). It is noted
below for a future port.

## Scope: What Was Filtered Out and Why

The range `v0.2.87..v0.2.110` contains 83 commits. Breakdown:

| Category | Count | Go action | Reason |
|---|---|---|---|
| `chore: bump bundled CLI version` (2.1.150→2.1.191) | 32 | skip | Pure metadata. Go does not bundle the CLI; it only asserts `MinimumCLIVersion = "2.0.0"` at runtime. |
| `docs: update changelog` / `chore: release` | 46 | skip | Release housekeeping. |
| `ebecbbc` Port session_store to anyio for trio (v0.2.88, #990) | 1 | skip | Python async-backend concern. Go uses goroutines/channels; the mirror batcher (`internal/query.go`) has no asyncio/trio equivalent and no behavioral change. |
| `3f50b5e` Pin mcp below 2.0.0 (v0.2.96, #1028) | 1 | skip | Python dependency management. Go has no equivalent dependency. |
| `113f359` Run the test suite under both asyncio and trio (#1021) | 1 | skip | Python test-suite infrastructure (parametrizing the suite over async backends). No Go equivalent. |
| `e68beb2` Switch e2e CI jobs to workload identity federation (#1018) | 1 | skip | GitHub Actions / auth infrastructure for the Python repo's CI. Not applicable to the Go port. |
| **`141c37f` Expose terminal task_updated events as typed TaskUpdatedMessage (v0.2.101, #1016)** | **1** | **port** | New typed message + parser branch. See §1. |

Rows sum to 83 = `git rev-list --count v0.2.87..v0.2.110`, confirming the scope analysis is exhaustive.

### Out of scope (post-v0.2.110, unreleased on Python `origin/main`)

- **`d47b180` Fix uncaught TypeError on string or non-dict message content (#1058)** — hardens
  content-block parsing. Go is already crash-safe here (`ParseContentBlocks` silently `continue`s
  on a non-dict block; `parseAssistantMessage` type-asserts `content` to `[]interface{}` and
  returns an error rather than crashing). The parity gap is behavioral: Python now **raises**
  `MessageParseError` on a non-dict block where Go **silently skips**. Deferred to a future port
  so this release can stay strictly at v0.2.110.

## Changes to Implement (in order)

### 1. Add TaskUpdatedMessage typed lifecycle message (Python v0.2.101, PR #1016)

**What**: The CLI emits `system`/`task_updated` events as a background task moves through
its lifecycle. Currently the Go parser has no `task_updated` branch, so these events fall
through to a generic `SystemMessage`. Add a typed `TaskUpdatedMessage` and a parser branch
that produces it.

Critical semantic: a background task's terminal state can arrive **only** as a
`TaskUpdatedMessage` whose `patch.status` is terminal — with no accompanying
`TaskNotificationMessage` (e.g. a task stopped via `TaskStop` reports `status="killed"`
here, and the matching notification is sometimes suppressed). Consumers tracking active
task IDs must therefore clear them on a terminal status from **either** a
`TaskNotificationMessage` or a `TaskUpdatedMessage`. To support this, expose a shared
`TerminalTaskStatuses` set spanning both lifecycle vocabularies.

**Design decisions (confirmed)**:
- `Status` is a **non-pointer `TaskUpdatedStatus` string type**; absent = `""`. A consumer
  checks terminality with `TerminalTaskStatuses[string(msg.Status)]`, which naturally
  returns `false` for `""`. This is preferred over `*TaskUpdatedStatus` for Go ergonomics.
- `TerminalTaskStatuses` is a **`map[string]bool`** keyed by raw status string, so it
  accepts values from both `TaskNotificationStatus` (`"stopped"`) and `TaskUpdatedStatus`
  (`"killed"`) uniformly.
- The parser is **defensive and never returns an error** for a `task_updated` event — this
  deliberately diverges from the sibling task parsers (`parseTaskNotificationMessage` etc.)
  which raise `MessageParseError` on missing required fields. Mirrors Python's
  "parsing must never raise on a lifecycle event" intent.

**Files**:

- `types.go` (after `TaskNotificationMessage`, ~line 300):
  - `TaskUpdatedStatus string` type with constants `TaskUpdatedStatusPending` (`"pending"`),
    `TaskUpdatedStatusRunning` (`"running"`), `TaskUpdatedStatusPaused` (`"paused"`),
    `TaskUpdatedStatusCompleted` (`"completed"`), `TaskUpdatedStatusFailed` (`"failed"`),
    `TaskUpdatedStatusKilled` (`"killed"`).
  - `var TerminalTaskStatuses = map[string]bool{"completed": true, "failed": true, "stopped": true, "killed": true}`.
  - `TaskUpdatedMessage` struct: `TaskID string`, `Patch map[string]interface{}`,
    `Status TaskUpdatedStatus` (`omitempty`), `SessionID string` (`omitempty`),
    `UUID string` (`omitempty`), plus `messageMarker()` method. Standalone struct (does
    not embed `SystemMessage`), consistent with `TaskStartedMessage`/`TaskNotificationMessage`.

- `parser.go`:
  - In `parseSystemMessage` switch, add `case "task_updated": return parseTaskUpdatedMessage(data)`.
  - New `parseTaskUpdatedMessage(data)`:
    - `task_id`: `data["task_id"].(string)`, default `""` — no error.
    - `patch`: `data["patch"].(map[string]interface{})`; if absent/non-dict, default to
      `map[string]interface{}{}` — no error. Preserve raw patch verbatim.
    - `status`: read from `patch["status"].(string)` (note: from `patch`, not `data`);
      absent → `""`.
    - `session_id`, `uuid`: `.(string)` from `data`, default `""` — no error.
    - Always `return msg, nil`.

**Expected outcome**: `ParseMessage` returns `*TaskUpdatedMessage` for `system`/`task_updated`
events. Consumers can do `TerminalTaskStatuses[string(msg.Status)]` to detect terminal
transitions and clear active-task tracking on either `TaskNotificationMessage` or
`TaskUpdatedMessage`. Malformed/missing fields never cause a parse error for this subtype.

**Tests** (`parser_test.go`, aligned with Python's parametrization in #1016):
- Full terminal patch (`status="killed"`, with `task_id`/`session_id`/`uuid`) → all fields
  populated; `TerminalTaskStatuses["killed"] == true`.
- Non-terminal `status="running"` → field set, `TerminalTaskStatuses["running"] == false`.
- Missing `patch` key → `Patch` is non-nil empty map; no panic, no error.
- `patch` is a non-dict (e.g. a string) → `Patch` is empty map; no error (defensive).
- `patch` present but without `status` → `Status == ""`; terminal check false.
- Missing `task_id`/`uuid`/`session_id` → each stays zero-value `""`; no error.
- Parametrized terminal statuses: `completed`/`failed`/`stopped`/`killed` all report terminal.
- `parseSystemMessage` dispatch: a `system`/`task_updated` payload yields `*TaskUpdatedMessage`,
  not a generic `*SystemMessage`.

## Version & Release

- `version.go`: bump `Version` from `"0.2.87"` to `"0.2.110"`.
- `CHANGELOG.md`: add `## [0.2.110] - 2026-07-01` entry. Note that v0.2.88-v0.2.110 is
  otherwise CLI bumps + a Python-only `anyio` port + a Python dep pin, and that the only
  functional Go change is `TaskUpdatedMessage` (#1016). Mention the deferred #1058.
- Tag `v0.2.110` after tests pass.

# Plan: Port Python SDK v0.1.76-v0.2.87 to Go SDK

## Summary

Port all functional changes from Python SDK v0.1.77 through v0.2.87 to the Go SDK.
The versions v0.1.77-v0.1.81 contain CLI bumps only. The major functional changes
are concentrated in v0.2.82. Versions v0.2.83-v0.2.87 are CLI bumps only.

## Changes to Implement (in order)

### 1. Export EffortLevel type alias (Python v0.2.82, PR #951)

**What**: Add a public `EffortLevel` type alias for the effort string values
(`"low"`, `"medium"`, `"high"`, `"xhigh"`, `"max"`). Update `AgentDefinition.Effort`
and `ClaudeAgentOptions.Effort` comments to reference the type.

**Files**:
- `types.go`: Add `EffortLevel` type alias and `EffortLevel*` constants
- `options.go`: Update `Effort` field comment

**Expected outcome**: Downstream consumers can use `claude.EffortLevel` for type-safe
effort configuration.

**Tests**: Add `TestEffortLevelValues` to verify the type alias and its valid values.

### 2. Suppress redundant ProcessError after error result (Python v0.1.77, PR #918)

**What**: When the CLI emits a result with `is_error=True` and then exits non-zero,
the trailing `ProcessError` now carries the structured error text from the result
instead of the generic "exit code 1" message. Track `lastErrorResultText` state
in the Query reader.

**Files**:
- `internal/query.go`: Add `lastErrorResultText` field to `Query` struct. Update
  `readMessages` to track error result text, reset it on non-result messages, and
  replace `ProcessError` with actionable text when appropriate.

**Expected outcome**: Consumers receive actionable error messages like
"Claude Code returned an error result: Reached maximum number of turns (60)"
instead of the generic "command failed (exit code: 1)".

**Tests**: Add tests for:
- ProcessError after error result uses result error text
- ProcessError after error result falls back to subtype when no errors array
- ProcessError after error result joins multiple errors
- ProcessError without prior result still surfaces
- ProcessError after success result still surfaces
- Non-result, non-session_state_changed message resets error text

### 3. Isolate stderr callback failures (Python v0.2.82, PR #932)

**What**: Wrap the stderr callback invocation in a try-recover block so a
panicking callback does not terminate the stderr read loop. Log the panic
and continue reading subsequent lines.

**Files**:
- `internal/transport/subprocess.go`: Update `readStderr` to recover from
  panics in the stderr callback per-line, logging them instead of terminating
  the loop.

**Expected outcome**: A failing stderr callback no longer silently drops all
subsequent stderr lines for the rest of the session.

**Tests**: Add test `TestStderrCallbackPanicDoesNotTerminateLoop` that verifies
all lines are received despite the callback panicking on the first one.

### 4. Tighten permission_suggestions type (Python v0.2.82, PR #955)

**What**: Change `HookInput.PermissionSuggestions` type from `[]interface{}`
to `[]map[string]interface{}` for type safety. Update parsing in
`handleHookCallback`.

**Files**:
- `internal/query.go`: Change `HookInput.PermissionSuggestions` type from
  `[]interface{}` to `[]map[string]interface{}`. Update parsing code.
- `types.go`: Change `HookInput.PermissionSuggestions` type from `[]interface{}`
  to `[]map[string]interface{}` in the public type.

**Expected outcome**: Consumers get properly typed permission suggestions instead
of raw `interface{}` values.

**Tests**: Update existing tests to use the new type. Add a test verifying
permission_suggestions are properly typed as `[]map[string]interface{}`.

### 5. Handle CancelledError in eager-flush done callback (Python v0.2.82, PR #931)

**What**: The mirror batcher's done callback (in the Python SDK it uses asyncio
tasks) must handle cancelled tasks gracefully. In Go, this maps to ensuring the
batcher's goroutine handles context cancellation without noisy log output.

**Files**:
- `internal/mirror_batcher.go`: Verify that the batcher handles context
  cancellation gracefully (it already does via `b.done` channel).

**Expected outcome**: No noisy error logging when the SDK shuts down with pending
eager flushes. (The Go implementation already handles this correctly via channel
semantics, so this is a verification pass.)

**Tests**: Add test verifying batcher handles close with pending items gracefully.

### 6. Update version and CHANGELOG

**What**: Bump version to 0.2.87, update CHANGELOG.md with all changes.

**Files**:
- `version.go`: Update `Version` constant to `"0.2.87"`
- `internal/transport/version.go`: Update `sdkVersion` to `"0.2.87"`
- `CHANGELOG.md`: Add entry for v0.2.87

**Expected outcome**: Version numbers reflect the ported Python SDK version.

## New Unit Tests to Add

1. `types_test.go`:
   - `TestEffortLevelValues` - verify EffortLevel constants

2. `internal/query_test.go`:
   - `TestProcessErrorAfterErrorResultUsesResultErrorText`
   - `TestProcessErrorAfterErrorResultFallsBackToSubtype`
   - `TestProcessErrorAfterErrorResultJoinsMultipleErrors`
   - `TestProcessErrorWithoutResultStillSurfaces`
   - `TestProcessErrorAfterSuccessResultStillSurfaces`
   - `TestNonResultMessageResetsErrorResultText`

3. `internal/transport/subprocess_test.go`:
   - `TestStderrCallbackPanicDoesNotTerminateLoop`

4. `types_test.go`:
   - `TestHookInputPermissionSuggestionsType`

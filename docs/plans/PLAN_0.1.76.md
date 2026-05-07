# Port Plan: Python SDK v0.1.73 -> v0.1.76

## Overview
Port all SDK-relevant changes from Python SDK v0.1.73..v0.1.76 to the Go SDK.
Target Go SDK version: v0.1.76.

## Changes to implement (in order)

### 1. [ALREADY DONE] fix(sessions): scan head for created_at timestamp, not just first line
- The Go SDK already has this fix in `parseSessionInfoFromLite` (sessions.go ~line 579), which scans `head` not `firstLine`.
- Verify by checking the existing code; no changes needed.

### 2. feat: add strict_mcp_config option (#915)
- Add `StrictMCPConfig bool` to `ClaudeAgentOptions` (options.go).
- Add `StrictMCPConfig` to `TransportOptions` (internal/transport/options.go).
- Wire it in `convertToTransportOptions` (query.go).
- Add `--strict-mcp-config` flag in `buildCommand` (internal/transport/subprocess.go).
- Tests: options_test.go (option present), transport subprocess_test.go (flag appears in command).

### 3. feat: terminate live CLI subprocesses on parent exit (#916)
- In Go, the equivalent is using `os/exec.Cmd` with process group management.
- The Go SDK already has graceful shutdown in `Close()` with SIGTERM -> SIGKILL.
- Add `atexit`-like cleanup via `runtime.SetFinalizer` or track processes in a package-level set.
- Actually, in Go the better approach is to set `SysProcAttr.Setpgid` and kill the process group on close, plus register cleanup.
- Add a package-level set of active processes and an `init()` function that registers a finalizer/cleanup.
- Tests: Verify subprocess is tracked and cleaned up.

### 4. fix(query): close receive stream on disconnect
- The Go SDK uses channels, not anyio streams, so this Python-specific fix does not apply directly.
- In Go, channels are garbage collected; no ResourceWarning equivalent.
- SKIP (Go-specific architecture difference).

### 5. feat(types): add updatedToolOutput to PostToolUseHookSpecificOutput (#911)
- In Go, `HookOutput.HookSpecificOutput` is `map[string]interface{}`, so `updatedToolOutput` is just a key.
- No structural change needed -- it's already a passthrough map.
- Add documentation to `HookOutput` about the `updatedToolOutput` key.
- Tests: test that hook output with `updatedToolOutput` passes through.

### 6. feat(types): add "xhigh" to effort Literal (#914)
- Update comment on `ClaudeAgentOptions.Effort` to include "xhigh".
- Update comment on `AgentDefinition.Effort` to include "xhigh".
- The Go SDK uses `string` type for effort (not a sum type), so no type change needed.
- Tests: transport subprocess_test.go for `--effort xhigh` flag.

### 7. feat: forward decision_reason and permission-display fields to ToolPermissionContext
- Add fields to `ToolPermissionContext` (types.go): `BlockedPath`, `DecisionReason`, `Title`, `DisplayName`, `Description`.
- Add fields to `HookInput` for PermissionRequest (types.go internal).
- Wire fields in `handleCanUseTool` (internal/query.go).
- Wire fields in the public-to-internal conversion in client.go and query.go.
- Tests: query_test.go for can_use_tool callback receiving new fields.

### 8. feat: support "defer" hook decision and ResultMessage.deferred_tool_use_ids
- Add `DeferredToolUse` struct in types.go.
- Add `DeferredToolUse *DeferredToolUse` to `ResultMessage`.
- Update `parseResultMessage` in parser.go to extract `deferred_tool_use`.
- Add `HookOutput.Decision` documentation for "defer" value.
- Tests: parser_test.go for parsing deferred_tool_use.

### 9. feat: add include_hook_events option (#917)
- Add `IncludeHookEvents bool` to `ClaudeAgentOptions`.
- Add to `TransportOptions` and wire through.
- Add `--include-hook-events` flag in `buildCommand`.
- Add `HookEventMessage` type in types.go.
- Update `ParseMessage` / `parseSystemMessage` in parser.go to detect hook_started/hook_response.
- Tests: parser_test.go for HookEventMessage parsing, options/transport tests.

### 10. fix: deserialize permission_suggestions into PermissionUpdate instances
- The Go SDK's `PermissionUpdate` already has full fields. Need to add a `PermissionUpdateFromMap` helper.
- Update `handleCanUseTool` in internal/query.go to deserialize properly.
- Tests: query_test.go for permission suggestion roundtrip.

### 11. feat: surface api_error_status on ResultMessage (#923)
- Add `APIErrorStatus *int` to `ResultMessage` in types.go.
- Update `parseResultMessage` in parser.go to extract `api_error_status`.
- Tests: parser_test.go for api_error_status parsing.

### 12. chore: bump bundled CLI version to 2.1.132
- Update `transport/version.go` sdkVersion constant (if tracked).
- No CLI version constant in Go SDK (the Go SDK does not bundle the CLI).

## Implementation order
1. Version bumps and documentation updates (items 1, 6, 12)
2. New option fields (items 2, 9)
3. New types and parser changes (items 8, 11)
4. Permission context enrichment (items 7, 10)
5. Hook output documentation (item 5)
6. Process cleanup (item 3)
7. Final: update CHANGELOG.md, version.go

## New tests needed
- `options_test.go`: StrictMCPConfig, IncludeHookEvents
- `parser_test.go`: DeferredToolUse in ResultMessage, APIErrorStatus in ResultMessage, HookEventMessage
- `query_test.go`: permission callback receives decision_reason/blocked_path/title/display_name/description, permission suggestions roundtrip
- `subprocess_test.go`: --strict-mcp-config flag, --include-hook-events flag, --effort xhigh

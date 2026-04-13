# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.57] - 2026-04-13

### Added

- **MCP large output test file**: Added `internal/transport/mcp_large_output_test.go` documenting the two-layer CLI spill mechanism and confirming SDK env-var handling — ported from Python SDK `test_mcp_large_output.py`. Tests cover `MAX_MCP_OUTPUT_TOKENS` passthrough, `CLAUDECODE` stripping, `CLAUDE_AGENT_SDK_VERSION` invariants, and layer-2 threshold boundary documentation (#756)

### Changed

- **`AgentDefinition.Effort` supports integer values**: Changed `Effort` field type from `string` to `interface{}` so it now accepts both string literals (`"low"`, `"medium"`, `"high"`, `"max"`) and numeric integer effort budgets — aligned with Python SDK where `effort: Literal[...] | int | None` (#782)

### Test Coverage

- **types_test.go**: +2 tests — `TestAgentDefinition_EffortAsInt`, `TestVersion`
- **internal/transport/subprocess_test.go**: +3 tests — `TestSDKVersionAlwaysSet`, `TestSDKVersionNotOverridableByUserEnv`, `TestMAXMCPOutputTokensPassthrough`
- **internal/transport/mcp_large_output_test.go**: +11 tests — `TestLayer1*` (3), `TestEnvInheritedFromOSEnviron`, `TestOptionsEnvOverridesOSEnviron`, `TestCLAUDECODEStrippedInMCPTest`, `TestSDKManagedVarsAlwaysSet`, `TestSDKVersionCannotBeOverriddenByUserEnvInMCPTest`, `TestLayer2*` (3)
- Total: 347 tests passing across all packages

## [0.1.56] - 2026-04-13

### Added

- **SDK version constant**: Added top-level `Version = "0.1.57"` constant and `internal/transport/version.go` (`sdkVersion`) so callers and the subprocess layer can reference the current SDK version without circular imports
- **`CLAUDE_AGENT_SDK_VERSION` env var**: The subprocess now always sets `CLAUDE_AGENT_SDK_VERSION` in the CLI subprocess environment after user-provided env, matching Python SDK behavior. User env cannot override this value

## [0.1.55] - 2025-04-06

### Added

- **AgentDefinition fields**: Added `Background` (`*bool`), `Effort` (`string`: "low"/"medium"/"high"/"max"), and `PermissionMode` (`PermissionMode`) fields to `AgentDefinition` — ported from Python SDK v0.1.54 (#782)
- **MCP MaxResultSizeChars**: Added `MaxResultSizeChars` field to `ToolAnnotations`. When set, the SDK forwards it as `_meta["anthropic/maxResultSizeChars"]` in `tools/list` responses to bypass Zod annotation stripping in the CLI — ported from Python SDK v0.1.55 (#756)

### Bug Fixes

- **Deadlock in standalone Query/QuerySync**: Fixed a deadlock where the `Query()` goroutine would hang indefinitely after receiving a `ResultMessage`. The circular dependency was: goroutine exit → `q.Close()` → stdin EOF → CLI exits → stdout EOF → channels close → goroutine exit. The fix calls `q.EndInput()` after forwarding a `ResultMessage`, breaking the cycle by closing stdin immediately so the CLI can exit gracefully — equivalent to Python SDK v0.1.53 (#780)
- **Setting sources empty string**: Fixed `--setting-sources` being passed as an empty string when not configured, which caused the CLI to misparse subsequent flags. The flag is now omitted entirely when `SettingSources` is nil or empty — ported from Python SDK v0.1.53 (#778)

### Test Coverage

- **query_test.go**: +2 tests — `TestQueryDeadlockRegression`, `TestQuerySyncDeadlockRegression`
- **types_test.go**: +7 tests — `TestAgentDefinition_BackgroundField`, `TestAgentDefinition_BackgroundOmittedWhenNil`, `TestAgentDefinition_EffortField`, `TestAgentDefinition_EffortOmittedWhenEmpty`, `TestAgentDefinition_PermissionModeField`, `TestAgentDefinition_AllNewFieldsCombined`
- **internal/transport/subprocess_test.go**: +3 tests — `TestSettingSourcesOmittedWhenNil`, `TestSettingSourcesOmittedWhenEmpty`, `TestSettingSourcesPassedWhenPopulated`
- **internal/sdk_mcp_integration_test.go**: +1 test — `TestToolAnnotations_MaxResultSizeChars`
- Total: 438 tests passing across all packages

## [0.1.52] - 2025-03-30

### Added

- **Context usage fields**: Added 8 new fields to `ContextUsageResponse`: `AutoCompactThreshold`, `DeferredBuiltinTools`, `SystemTools`, `SystemPromptSections`, `SlashCommands`, `Skills`, `MessageBreakdown`, `APIUsage` — aligned with Python SDK v0.1.52 (#764)
- **Typed GetContextUsage return**: Changed `ClaudeSDKClient.GetContextUsage()` return type from `map[string]interface{}` to `*ContextUsageResponse` for type-safe access to context usage data — aligned with Python SDK v0.1.52 (#764)
- **SdkBeta type**: Added `SdkBeta` type alias and `SdkBetaContext1M` constant for typed beta feature flags. Changed `Betas` field in `ClaudeAgentOptions` from `[]string` to `[]SdkBeta` (backward-compatible type alias)
- **Session mutations**: Added `ForkSession()`, `DeleteSession()`, `TagSession()`, `RenameSession()` functions with full Unicode sanitization support — ported from Python SDK v0.1.49–v0.1.51 (#668, #670, #744)
- **AgentDefinition fields**: Added `Skills`, `Memory`, `McpServers` (v0.1.49), `DisallowedTools`, `MaxTurns`, `InitialPrompt` (v0.1.51) fields with camelCase JSON tags (#684, #759)
- **SDKSessionInfo fields**: Added `Tag`, `CreatedAt`, and `FirstPrompt` fields to `SDKSessionInfo` — ported from Python SDK v0.1.50 (#667)
- **RateLimitEvent**: Added typed `RateLimitEvent` message with all rate-limit fields — ported from Python SDK v0.1.49 (#648)
- **AssistantMessage usage**: Preserved per-turn `Usage` on `AssistantMessage` for token tracking — ported from Python SDK v0.1.49 (#685)
- **ResultMessage fields**: Added `Errors` field and preserved dropped fields for forward compatibility — ported from Python SDK v0.1.51 (#718, #749)
- **SystemPromptFile**: Added `SystemPromptFile` option to `ClaudeAgentOptions` for `--system-prompt-file` CLI flag — ported from Python SDK v0.1.51 (#591)
- **Effort option**: Added `Effort` field to `ClaudeAgentOptions` for controlling thinking depth — ported from Python SDK v0.1.48

### Bug Fixes

- **Fine-grained tool streaming**: Fixed `IncludePartialMessages=true` not delivering `input_json_delta` events by enabling the `CLAUDE_CODE_ENABLE_FINE_GRAINED_TOOL_STREAMING` environment variable in the subprocess
- **Forward-compatible message parsing**: Unknown message types are silently skipped instead of causing errors
- **Context cancellation in control handlers**: `handleCanUseTool` and `handleHookCallback` now properly check context cancellation before executing callbacks, ensuring `control_cancel_request` messages from the CLI actually interrupt in-flight operations — ported from Python SDK v0.1.52 (#751)

### Test Coverage

- **types_test.go**: +16 tests — PermissionMode constants, McpServerStatus (connected/minimal/failed/proxy/wrapper/round-trip), AgentDefinition JSON serialization with camelCase verification, ContextUsageResponse new fields, SdkBeta constants
- **sessions_test.go**: +35 tests — `extractFirstPromptFromHead`, `ListSessions` (15 scenarios), `GetSessionMessages` (14 scenarios), `BuildConversationChain`
- **session_mutations_test.go**: +25 tests — `appendToSession`, `RenameSession`, `TagSession`, `SanitizeUnicode`, `DeleteSession`, `ForkSession` (10 scenarios)
- **client_streaming_test.go**: +9 tests — MCP reconnect/toggle/stop/status control requests, typed `GetContextUsage` response validation
- **internal/query_test.go**: +6 tests — `TestCancelRequestCancelsInflightHook`, `TestCancelRequestForUnknownIDIsNoop`, `TestCompletedRequestRemovedFromInflight`, `TestCancelRequestPreventsResponse`, `TestHandleCanUseToolWithCancelledContext`, `TestHandleHookCallbackWithCancelledContext`
- Total: 426 tests passing across all packages

## [0.1.46] - 2025-03-05

### Added

- **Task message types**: Added `TaskStartedMessage`, `TaskProgressMessage`, `TaskNotificationMessage` types for handling task lifecycle events. Includes `TaskUsage` struct and `TaskNotificationStatus` constants.
- **MCP status types**: Added `McpServerConnectionStatus`, `McpToolAnnotations`, `McpToolInfo`, `McpServerInfo`, `McpSdkServerConfigStatus`, `McpClaudeAIProxyServerConfig`, `McpServerStatus`, and `McpStatusResponse` types.
- **MCP control methods**: Added `ReconnectMCPServer()`, `ToggleMCPServer()`, and `StopTask()` methods to `ClaudeSDKClient`.
- **Session management**: Added `ListSessions()` and `GetSessionMessages()` functions for reading session history. Includes `SDKSessionInfo` and `SessionMessage` types.
- **Hook subagent context**: Added `agent_id` and `agent_type` fields to `HookInput` for tool-lifecycle hooks (PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest).

### Changed

- **GetMCPStatus return type**: Changed from `map[string]interface{}` to typed `*McpStatusResponse`.
- **ResultMessage**: Added `StopReason` field.

## [0.1.40] - 2025-02-24

### Bug Fixes

- **Unknown message type handling**: Fixed an issue where unrecognized CLI message types (e.g., `rate_limit_event`) would crash the session by returning errors from `ParseMessage`. Unknown message types are now silently skipped (returning `(nil, nil)`), making the SDK forward-compatible with future CLI message types. This aligns with the Python SDK behavior in version 0.1.40.

### Added

- **Forward compatibility tests**: Added comprehensive tests in `parser_rate_limit_test.go` to verify that unknown message types (including `rate_limit_event`) are properly handled without crashing the SDK.
- **Updated test expectations**: Modified `TestParseInvalidMessage` and `TestMessageParseErrorContainsData` to align with the new forward-compatible behavior.

### Changed

- `ParseMessage()` now returns `(nil, nil)` for unknown message types instead of returning an error
- All callers of `ParseMessage()` in `client.go` and `query.go` now properly handle `nil` message returns

## [0.1.36] - 2024-12-19

### Added

- Initial release of the Go SDK
- Feature parity with Python SDK 0.1.36
- Support for bidirectional streaming communication with Claude Code CLI
- MCP (Model Context Protocol) server support
- Custom tools and hooks support
- Structured outputs support
- Session management and forking
- Permission management
- File checkpointing and rewind functionality

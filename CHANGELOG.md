# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.143] - 2026-08-24

This release ships two bodies of work: **(A)** the port of Python SDK
v0.2.132..v0.2.143, and **(B)** Go-only feature work (below) that puts the
in-process MCP support on `github.com/modelcontextprotocol/go-sdk` (v1.7.0),
closing the last 1:1-port gap with the Python SDK —
`McpSdkServerConfig["instance"]` being a real MCP server users can
hand-build.

### Added (Go-only: real MCP types via go-sdk)

- **`MCPSdkServerConfig.Instance *mcp.Server`**: the previously opaque
  `Instance interface{}` is now a real `*mcp.Server`
  (`github.com/modelcontextprotocol/go-sdk/mcp`). `CreateSdkMcpServer` keeps
  its signature and populates `Instance` with a factory-built server, and
  users may instead assign a hand-built `*mcp.Server` directly: resources,
  prompts, and custom methods then reach the CLI verbatim over the control
  channel, at full go-sdk fidelity. Additive for users of
  `CreateSdkMcpServer`; code that type-asserted `Instance` to
  `*internal.MCPServer` must switch to the `*mcp.Server` value.
- **New dependency**: `github.com/modelcontextprotocol/go-sdk` v1.7.0
  (plus its transitive modules: `jsonschema-go`, `golang-jwt/jwt`,
  `segmentio/encoding`, `oauth2`, `x/tools`, `uritemplate`, `x/time`,
  `go-cmp`).

### Changed (Go-only: real MCP types via go-sdk)

- **SDK MCP bridge rewritten over go-sdk's in-memory transport**
  (`internal/sdk_mcp_bridge.go`): the hand-rolled JSON-RPC dispatch is
  replaced by one `*mcp.Server` session per server over
  `mcp.NewInMemoryTransports()`, with pending-waiter response routing —
  mirroring Python's `SdkMcpBridge` (#1218) one architectural level up.
  Factory wire semantics are preserved via a receiving middleware on the
  factory-built server: `initialize` echoes the client's `protocolVersion`
  with capabilities `{"experimental": {}, "tools": {"listChanged": false}}`,
  `tools/list`/`tools/call` keep the exact payloads (annotation spellings,
  `maxResultSizeChars` in `_meta`, `isError` always present, content
  conversion, validation, panic mapping), and `resources/*`/`prompts/*` on a
  tools-only factory server are refused with `-32601`. go-sdk's own result
  marshaling cannot express these (`isError` is `omitempty`, tool
  annotations always carry hint defaults), so the middleware renders those
  payloads itself; every other method is dispatched by go-sdk.
  Server-initiated traffic is handled like Python: `ping` answered with
  `{}`, other server→client requests refused with `-32601` ("... is not
  supported for SDK servers"), server→client notifications dropped.
- **Bridge lifecycle is now per-`Query`**: bridge sessions own goroutines,
  so the package-level registry was replaced by a registry on the `Query`
  that `Close` drains (bounded 5s grace, mirroring Python's
  `SHUTDOWN_GRACE_SECONDS`, then abandon).

### Behavior deltas (wire-visible, go-sdk-legitimated)

- The MCP handshake is now enforced: requests other than
  `initialize`/`ping` sent before `initialize` are refused instead of being
  served (the CLI always initializes first, so real sessions are
  unaffected).
- `initialize` protocol negotiation follows go-sdk: a client
  `protocolVersion` go-sdk does not support now negotiates to the latest
  legacy version instead of being echoed verbatim; supported versions
  (including `2024-11-05`, `2025-03-26`, `2025-06-18`) are echoed as
  before, and a missing `protocolVersion` still defaults to `2024-11-05`.
- A repeated `initialize` on a live session is answered from the first
  handshake's result (go-sdk rejects duplicate handshakes; the CLI
  re-initializes SDK servers at turn starts). In-flight calls are
  undisturbed.
- The `-32601` error for `resources/*`/`prompts/*` on a tools-only factory
  server now carries go-sdk's message text (`method not found: "<method>"`)
  instead of `Method '<method>' not found`; the code is unchanged.
- Integral numbers on the wire now decode as Go `int` inside the SDK
  (JSON round-trips make every number a `float64`; re-marshaling renders
  integral floats identically, so the CLI-facing bytes are unchanged).

---

**(A)** Port of Python SDK v0.2.132..v0.2.143 changes. Of the 11 tagged releases in
this range, nine non-bump commits were reviewed: all nine were ported (one,
#1218, partially — see "Not Ported"). Bundled CLI version bumps
(2.1.225 → 2.1.238) and changelog/release housekeeping are not applicable —
the Go SDK does not bundle a CLI.

### Added

- **`ConversationResetMessage` (#1196)**: the CLI's `conversation_reset` frame now parses into a dedicated message type with `NewConversationID`, `UUID`, and `SessionID`, letting applications detect when a `/clear` or other transcript-discarding flow resets the conversation mid-session. This widens the `Message` union — exhaustive switches need a new case. Ported from Python SDK #1196 (v0.2.137, commit `54dd3b4`).
- **Message origin on `UserMessage` and `ResultMessage` (#1199)**: new `Origin` field surfaces why a turn was initiated — distinguishing application-submitted prompts (`"human"`) from background-task notifications, scheduled triggers, peer messages, and other session-injected turns. New exported types: `MessageOrigin` (pass-through map with `Kind()`/`Subkind()` accessors), `MessageOriginKind`, `TaskNotificationOriginSubkind`. Ported from Python SDK #1199 (v0.2.137, commit `d48fa33`).
- **`ResumeSessionAt` / `ResumeDropsTurn` options for truncating resume (#1198)**: fork a session at an earlier transcript entry (`--resume-session-at=<uuid>`) and validate that only entries from a specific turn are discarded (`--resume-drops-turn=<prompt uuid>`). Both are forwarded in equals form with the same Windows cmd-metacharacter rejection as `Resume`/`SessionID`; `ResumeDropsTurn` is a `*string` forwarded whenever non-nil — an empty string reaches the CLI and is rejected as malformed rather than silently disarming the guard. Ported from Python SDK #1198 (v0.2.137, commit `be2d0df`).
- **`ResultError` (#1205)**: when the CLI exits after a terminal error result, the SDK now returns a typed `ResultError` (embedding `ProcessError`) instead of a bare "exit code 1" error. Carries `Subtype`, `Errors`, `Result`, `APIErrorStatus`, `TerminalReason`, `SessionID`, and the raw `Data` payload so callers can branch on the failure reason without string matching. `IsResultError` works through wrapping; `IsProcessError` also matches `*ResultError` (mirroring the Python subclass relationship). Ported from Python SDK #1205 (v0.2.140, commit `90ab957`).
- **`CanUseTool` with `Query()` and string prompts (#1204)**: the permission callback no longer requires the streaming client — a new internal `configureCanUseTool` step auto-routes the permission prompt tool over stdio when a callback is set, and returns an error when `CanUseTool` is combined with `PermissionPromptToolName` (mutually exclusive). Ported from Python SDK #1204 (v0.2.140, commit `fcdae22`).
- **`ForwardSubagentText` option (#1206)**: forwards a subagent's text and thinking blocks as messages in the stream so consumers can render the full nested transcript (sent as the `forwardSubagentText` initialize capability). Matches the TypeScript SDK's `forwardSubagentText`. Ported from Python SDK #1206 (v0.2.140, commit `c97420c`).
- **Subagent transcript parentage (#1207)**: `GetSubagentMessages` and `GetSubagentMessagesFromStore` now recover `ParentToolUseID` from the subagent's metadata (`.meta.json` sidecar / synthetic `agent_metadata` entry, last wins), linking each subagent's messages to the Agent `tool_use` block in the parent session. `SessionMessage` also gains `ParentAgentID` for the spawning agent's id. Ported from Python SDK #1207 (v0.2.140, commit `2bbdce6`).
- **SDK MCP server full-fidelity JSON-RPC bridge (#1218)**: the hand-rolled MCP dispatch was replaced by a bridge (`internal/sdk_mcp_bridge.go`) mirroring mcp's own in-memory transport semantics: `initialize` echoes the client's `protocolVersion` and reports real capabilities; `ping` is answered; `resources/*` and `prompts/*` return `-32601` (tools-only server, exactly what the Python factory server answers); `notifications/cancelled` settles the in-flight request with `-32800`; request-id reuse in flight is refused; tool arguments are validated against `inputSchema` before the handler runs; tool results pass through with `isError` (camelCase, always present on success), `resource_link`/text-resource flattening, and panics mapped to `isError` results. New public API: `SdkMcpTool.Annotations`, `ToolBuilder.WithAnnotations`, and the `ToolAnnotations` alias — map-form annotations accept camelCase or snake_case hint names, and wire annotations carry only the four standard hints (`maxResultSizeChars` stays in `_meta`). Ported from Python SDK #1218 (v0.2.140, commit `0f005fa`).

### Fixed

- **Pending control requests receive the real error text on failed resume (#1198)**: when the CLI rejects a resume (nonexistent session, `--resume-drops-turn` guard failure) it prints an error result then exits 1 before answering `initialize`; pending control requests now fail fast with the `Claude Code returned an error result: ...` text instead of a bare "exit code 1" (or hanging until timeout).
- **`settings.json` seeded into the temp config dir on `SessionStore` resume (#1197)**: resume now copies `settings.json` and `cowork_settings.json` alongside the credentials (stripping `enabledPlugins`/`extraKnownMarketplaces`/`env.CLAUDE_CONFIG_DIR`), preserving `apiKeyHelper` auth, user hooks, env vars, and permissions. Previously hosts authenticating solely via `apiKeyHelper` failed with "Not logged in" on resume. Seed files that are directories/FIFOs/unreadable are skipped with a warning instead of aborting the resume. Ported from Python SDK #1197 (v0.2.137, commit `b4d65f5`).
- **Trailing stream errors no longer lost in `Query`**: the read loop forwards errors before closing the message channel, but the main `select` could observe the close first and drop the error; the loop now drains `Errors()` non-blockingly at stream end.
- **Test-suite livelock in `internal/transport`**: the `setupTestTransport` cleanup drain loop spun forever once `readStdout` closed both channels (receive on a closed channel always won over `default:`); it now checks the channel-closed flag.

### Breaking

- **SDK MCP wire semantics**: unknown tools and handler errors are now `isError` tool results (matching the Python SDK and real mcp servers) instead of JSON-RPC `-32601`/`-32603` errors; tool-result content is normalized (`is_error` → `isError`, `resource_link`/text resources flattened to text, unknown/binary content dropped with a warning); wire annotations are camelCase-only. Callers that matched on the old JSON-RPC error codes for tool failures should check `isError` results instead.
- The `Message` union gained `ConversationResetMessage` — exhaustive switches over `Message` need a new case.

### Not Ported (analyzed, ruled out)

- **#1218 mcp 1.x/2.x compatibility shim (`_mcp_compat.py`)**: the Go SDK has no mcp library dependency — its in-process MCP server is hand-rolled Go, so per-major-version compat has no equivalent. Hand-built `mcp.server.Server` pass-through and server-lifespan semantics are likewise N/A (Go MCP servers are factory funcs + handlers). The user-visible wire behaviors were ported (see above).
- **Python `stream_input` "prompt iterable raised → close stdin" guard (#1204)**: Go channel prompts cannot raise, and Go's transport never closes stdin early, so no equivalent exists or is needed. Go's one-shot `Query` already holds stdin open until a result with no in-flight tasks — strictly stronger than the Python `wait_for_result_and_end_input` fix.

### Test Coverage

Statement coverage is now **≥95% in every library package** (root 97.3%, `internal` 99.9%, `internal/transport` 98.8%), up from 77.5%/88.7%/84.7% before this release:

- Port tests mirroring the new Python tests: conversation_reset parsing, origin parsing (all kinds/subkinds/malformed), `ResultError` payload/normalization/wrapping, `--resume-session-at=`/`--resume-drops-turn=` argv + Windows metacharacter rejection + empty-string forwarding, typed-error wiring (incl. pending-initialize fast fail), `configureCanUseTool` (mutual exclusion, stdio routing), `forwardSubagentText` initialize capability, settings seeding (BOM, overflow float, FIFO/unreadable seeds), subagent parent-id recovery (sidecar + store metadata, last-wins), and the full SDK MCP bridge suite (handshake, cancellation, id reuse, argument validation, content conversion, annotations spellings).
- New `coverage_*_test.go` files across all three packages covering pre-existing gaps: every control-protocol method (`GetServerInfo`/`Interrupt`/`SetModel`/`StopTask`/etc.), `Client.Connect`/`Send`/`Close` paths, `Query` lifecycle paths, session listing/fork/import/mutation paths, resume candidate loading, transport Connect/Write/streamInput/readStdout/Close escalation, and the mirror batcher's retry/overflow/flush paths. Windows-only branches are exercised through the existing `goos` test seam where possible; the remaining uncovered lines are provably unreachable on POSIX (home-dir failure, never-erroring `canonicalizePath`, closed-channel arms nothing can close).

- `version.go` / `internal/transport/version.go`: bumped to `0.2.143`.
- Full suite: `go build ./...`, `go vet ./...`, `go test . ./internal ./internal/transport -race` all clean.

## [0.2.132] - 2026-08-07

Port of Python SDK v0.2.120..v0.2.132 changes. Of the 12 tagged releases in
this range, seven non-bump commits were reviewed: six required a Go port and
one was analyzed and explicitly ruled out. Bundled CLI version bumps
(2.1.212 → 2.1.224) and changelog/release housekeeping are not applicable —
the Go SDK does not bundle a CLI.

### Security

- **`--resume` / `--session-id` now passed as a single `--flag=value` argv token (#1123)**: the CLI declares `--resume` with an *optional* value, so in the two-token form a dash-leading value (e.g. `Resume: "--version"`) is not bound to the flag and is instead parsed as an independent CLI flag — an argv-level flag injection for applications that route untrusted input into `Resume` / `SessionID`. The equals form always binds the value; the CLI then rejects a dash-leading value as an invalid session ID, which is the desired behavior. Ported from Python SDK #1123 (v0.2.121, commit `347a1cb`).
- **Refuse to spawn `.bat`/`.cmd` CLI scripts on Windows (#1127)**: on Windows installs without a bundled `claude.exe`, CLI discovery can resolve npm's `claude.cmd` batch shim. Windows runs batch scripts by rewriting the spawn into `cmd.exe /c ...`, and cmd.exe re-parses the whole command line — Go's `os/exec` quotes for the MSVCRT argv rules only, so cmd.exe metacharacters inside an argument value (a `--resume` session title, `--mcp-config` JSON, a system prompt) reach cmd.exe unescaped and can execute injected commands (the "BatBadBut" class, CVE-2024-27980). No reliable escaping for cmd.exe exists, so `Connect` now refuses `.bat`/`.cmd` paths with a `CLIConnectionError` pointing at the alternatives (the native installer `irm https://claude.ai/install.ps1 | iex`, or an explicit `TransportOptions.CLIPath` to a `claude.exe`). The extension check classifies every path component with plain string logic (trailing dot/space stripping, NTFS alternate-data-stream specs, drive-relative paths, bare `.cmd` — the same normalization Rust applied for CVE-2024-24576). CLI discovery on Windows now prefers a native `claude.exe` over a shim PATH hit, probes only `~/.local/bin/claude.exe` as a fallback location, and the not-found error recommends the native installer instead of npm. Ported from Python SDK #1127 (v0.2.124, commit `879e920`).
- **Windows-only rejection of cmd.exe metacharacters in `Resume` / `SessionID` (#1127)**: defense in depth — on Windows these values now reject `& | < > ^ % ! "` and CR/LF at `Connect`, keeping them inert even if a cmd.exe hop is ever reintroduced between the SDK and the CLI. POSIX behavior is unchanged.
- **`ExtraArgs` binds dash-leading values via `--flag=value` (#1127)**: in the two-token form a dash-leading value parses as a separate flag when the CLI declares the option with an optional value — the same injection class #1123 closed for `--resume`. Values not starting with `-` and bare flags are unchanged.
- **Skill names in `Options.Skills` are validated before reaching `--allowedTools` (#1145)**: the CLI splits that value into permission rules on commas and spaces outside parentheses, and its tokenizer honors no escape sequences, so a name carrying a delimiter cannot round-trip. The transport now fails closed at `Connect`, rejecting parentheses, commas, control characters (C0, DEL, C1), U+FEFF, empty names, invalid UTF-8, a literal `*` and wildcard suffixes (`:*`, ` *`), and shapes that parse but can never match — surrounding whitespace, a leading `/`, consecutive backslashes, and a trailing unpaired backslash. A bare string passed in place of a list (or any other type) now errors instead of being silently ignored, with a `Did you mean ['name']?` suggestion. Ported from Python SDK #1145 (v0.2.129, commit `cbed47d`).

### Breaking

- `Options.Skills`: `[]string{"plugin:*"}` and `[]string{"*"}` now return an error — use `"all"`, or a `Skill(...)` rule in `AllowedTools` for prefix matching. `[]string{" name"}` and `[]string{"/name"}` now error as well; both previously built a rule that could never match, so the skill was silently unavailable. A bare string other than `"all"` (e.g. `Skills: "my-skill"`) now errors — pass `[]string{"my-skill"}`.
- `ResultMessage.ModelUsage` is now typed as `map[string]ModelUsage` instead of `map[string]interface{}` (see below).

### Added

- **`ResultMessage.TerminalReason` (#1142)**: surfaces why the query loop ended (`"completed"`, `"max_turns"`, `"aborted_streaming"`, `"aborted_tools"`, etc.). A value of `"aborted_streaming"` or `"aborted_tools"` means the turn was cancelled via `Client.Interrupt`. Empty when the CLI did not report a terminal reason (older CLI versions, or a result that bypassed the query loop). Ported from Python SDK #1142 (v0.2.126, commit `07b46c6`).
- **`ModelUsage` struct (#1143)**: `ResultMessage.ModelUsage` is now `map[string]ModelUsage`, mirroring the TypeScript SDK's `ModelUsage` shape (camelCase JSON keys, passed through verbatim from the CLI's `modelUsage` field): `InputTokens`, `OutputTokens`, `CacheReadInputTokens`, `CacheCreationInputTokens`, `WebSearchRequests`, `CostUSD`, `ContextWindow`, `MaxOutputTokens`, plus `CanonicalModel` and `Provider` (emitted by newer CLI versions; give callers a stable key for their own rate-table lookups across provider-specific model ids and aliases). Ported from Python SDK #1143 (v0.2.126, commit `9c27ca8`).

### Fixed

- **Stdin no longer closed on a result frame while tasks are in flight (#1103)**: a `result` frame marks the end of one *turn*, not the end of the *run* — a background task keeps running past it and still needs stdin for hook and SDK-MCP control responses. `Query` previously closed stdin on the first result frame, which broke a still-running subagent's SDK-MCP tool calls ("Stream closed") and silently bypassed its PreToolUse hooks. The SDK now tracks in-flight tasks from the `task_started` / `task_notification` / terminal `task_updated` lifecycle frames (only `local_agent` / `local_workflow` — types that reliably reach a terminal status — so background shells and monitors cannot hang the query) and only treats a result frame as run-ending when no tasks are in flight. With no background tasks, behavior is unchanged. Ported from Python SDK #1103 (v0.2.127, commit `e6e07f1`).
- **Concurrent `exec.Cmd.Wait` calls raced in `SubprocessTransport.Close`**: both `readStdout`'s teardown and `Close`'s graceful-shutdown path called `process.Wait()`, and `os/exec.Cmd.Wait` is not safe for concurrent use (surfaced by `go test -race`). Reaping is now serialized through a helper that runs `Wait` exactly once and hands later callers the stored result; `Close` also no longer reads `process.ProcessState` concurrently with an in-flight `Wait`.

### Not Ported (analyzed, ruled out)

- **#1117 — `CLAUDE_CLI_VERSION` validation / build-script hardening**: Python packaging scripts only (`scripts/download_cli.py`, `scripts/update_cli_version.py`, CI workflows); the Go SDK has no CLI-download build scripts, so there is no equivalent attack surface.

### Test Coverage

- `internal/transport/subprocess_security_test.go` (new): ~70 cases mirroring the new Python `tests/test_transport.py` coverage — `--resume=--evil` / `--session-id=-r` single-token regression (#1123); batch-path classification table (`claude.cmd:stream`, `claude:evil.cmd`, `C:claude.cmd`, bare `.cmd`, `claude.cmd\...\..`, trailing dots/spaces) with refusal before any spawn, Windows CLI discovery preferring a native exe, cmd.exe metacharacter rejection, `ExtraArgs` dash-leading value binding (#1127); parametrized skill-name rejection/acceptance tables and bare-string `Did you mean` errors (#1145). Windows branches are exercised through a `goos` test seam so they run on POSIX CI.
- `internal/transport/subprocess_extended_test.go`, `subprocess_test.go`: existing `--resume` / `--session-id` argv expectations updated to the equals form.
- `parser_test.go`: `TestParseResultMessageWithTerminalReason`, `TestParseResultMessageMissingTerminalReason` (#1142), and typed-field assertions in `TestParseResultMessageWithModelUsage` (#1143).
- `query_test.go`: `TestTaskLifecycleTracker` (add / non-deferring-type ignored / non-terminal keeps / terminal discards via both `task_notification` and `task_updated` / unknown-id / missing-id), `TestResultWithInflightTaskKeepsStdinOpen` (parametrized over both drain frames), and `TestResultWithNoInflightTasksClosesStdin` (unchanged behavior) (#1103).
- `version.go` / `internal/transport/version.go`: bumped to `0.2.132`.
- Full suite: `go build ./...`, `go vet ./...`, `go test ./...` all clean.

## [0.2.120] - 2026-07-16

Port of Python SDK v0.2.110..v0.2.120 changes. Of the 10 tagged releases in
this range, six non-bump commits were reviewed: two required a Go port and
four were analyzed and explicitly ruled out. CLI version bumps (2.1.202 →
2.1.211) and changelog/release housekeeping are not applicable — the Go SDK
does not bundle a CLI.

### Added

- **`can_use_tool` shadowed advisory warning (#1081)**: `can_use_tool` is only consulted when the CLI's permission ladder lands on "ask". `bypassPermissions`, or an `allowed_tools` entry that allows a whole tool (`"Read"`, `"Read()"`, `"Read(*)"`), auto-approves the call earlier in the ladder and the callback never runs — a silent security footgun (documented cases of path-jailing callbacks being bypassed). The SDK now emits a single `slog.Warn` advisory at `Query()` start and `ClaudeSDKClient.Connect()` when the callback is set alongside a visibly-shadowing option. `skills:"all"` is treated as injecting a bare `"Skill"` (shadows); `skills:[]string{...}` injects `Skill(name)` specifiers (does not shadow). Advisory only — it never blocks or returns an error. Ported from Python SDK #1081 (v0.2.111, commit `7968c40`).

### Fixed

- **Non-dict content block now raises `MessageParseError` (#1058)**: `ParseContentBlocks` previously `continue`d past a content block that was not a JSON object (e.g. a bare string or number inside the `content` array), silently dropping it. It now returns `MessageParseError` naming the offending Go type, matching Python #1058's "expected dict, got X". The `assistant` branch already rejected a bare-string `content` via its `[]interface{}` type assertion; that behavior is now locked in by tests. Ported from Python SDK #1058 (v0.2.111, commit `d47b180`; previously deferred at v0.2.110 where the parity gap was first noted).
- **`CLAUDE_AGENT_SDK_VERSION` env var was stale**: `internal/transport/version.go` still held `"0.2.87"` after the v0.2.110 port (only the top-level `version.go` was bumped), so the CLI received `CLAUDE_AGENT_SDK_VERSION=0.2.87`. Both constants are now `0.2.120`, and the existing code comment reminds that the two must stay in sync.

### Not Ported (analyzed, ruled out)

- **#1083 — NDJSON line framing**: Python's `anyio.TextReceiveStream` yields chunks (up to 64KiB), and treating each chunk as a line stripped whitespace at the seam — including inside JSON string values. Go reads stdout/stderr with `bufio.Scanner` (ScanLines), which frames by line natively; `strings.TrimSpace` then runs on a complete line and cannot eat interior whitespace. No change needed.
- **#1082 — zombie CLI children**: Python's async `close()` was not cancellation-safe; a `CancelledError` propagated past the terminate/kill escalation and leaked the child. Go's `Close()` is synchronous (uncancellable) and runs the full `wait(5s) → SIGTERM → wait(5s) → Kill` chain; the subprocess is also created with `exec.CommandContext`, which auto-kills on ctx cancel. No orphan is possible under either path.
- **#1084 — e2e stderr test fix**: CI test-infra fix (clean cwd / trust workspace); the Go SDK has no equivalent e2e suite.
- **#1116 — Slack notification workflow**: `.github/workflows/` change; the Go SDK ships no GitHub Actions workflows.

### Test Coverage

- `types_test.go`: +4 tests for non-dict content block handling — `ParseContentBlocks` raises on string/number/bool/nil blocks (parametrized), aborts parse even after a preceding valid block, assistant string-content raises, and both `user`/`assistant` branches raise on a non-dict block.
- `warnings_test.go` (new file): +14 tests covering `wholeToolAllowed` (table-driven), `canUseToolShadowedMessage` (bypass precedence, dedupe/order preservation, specifiers-not-shadowed, none), and `warnIfCanUseToolShadowed` (nil opts, no callback, no shadow, bypass, whole-tool entry, skills="all" injects Skill, skills list does not, no options mutation). Captures `slog` output via a test handler to assert emission and silence.
- `version.go` / `internal/transport/version.go`: bumped to `0.2.120`.
- Total: 733 tests passing across all packages.

## [0.2.110] - 2026-07-01

Port of Python SDK v0.2.88-v0.2.110 changes. Of the 83 commits in this range,
exactly one is a functional change requiring a Go port; the remainder are CLI
version bumps (32), changelog/release housekeeping (46), a Python-only
`anyio`/`trio` async-backend port (#990), a Python dependency pin (#1028), and
Python test/CI infrastructure (#1018, #1021).

### Added

- **`TaskUpdatedMessage` typed lifecycle message**: The CLI emits `system`/`task_updated` events as a background task moves through its lifecycle. Previously these fell through to a generic `SystemMessage`; they are now parsed into a typed `TaskUpdatedMessage` (standalone struct with `TaskID`, `Patch`, `Status`, `SessionID`, `UUID`). Ported from Python SDK #1016 (v0.2.101, commit `141c37f`).
- **`TaskUpdatedStatus` type and constants**: `TaskUpdatedStatusPending` ("pending"), `TaskUpdatedStatusRunning` ("running"), `TaskUpdatedStatusPaused` ("paused"), `TaskUpdatedStatusCompleted` ("completed"), `TaskUpdatedStatusFailed` ("failed"), `TaskUpdatedStatusKilled` ("killed").
- **`TerminalTaskStatuses` shared set**: A `map[string]bool` spanning both task lifecycle vocabularies (`"completed"`, `"failed"`, `"stopped"` from `TaskNotificationMessage`, `"killed"` from `TaskUpdatedMessage`). Consumers tracking active task IDs should clear them on a terminal status from EITHER a `TaskNotificationMessage` or a `TaskUpdatedMessage`.

### Changed

- **Parser dispatch**: `parseSystemMessage` now routes the `task_updated` subtype to `parseTaskUpdatedMessage`. This parser is deliberately defensive and NEVER returns an error, mirroring Python's "parsing must never raise on a lifecycle event" intent (it diverges from the sibling task parsers, which raise `MessageParseError` on missing required fields). `Patch` defaults to a non-nil empty map when absent or non-dict; `Status` is read from `patch.status` (not `data.status`) and defaults to `""`.

### Deferred

- **#1058** (post-v0.2.110, unreleased on Python `origin/main`): hardens content-block parsing to raise `MessageParseError` on a non-dict block. Go is already crash-safe here but the parity gap (Go silently skips where Python now raises) is deferred to a future port so this release stays strictly at v0.2.110.

### Test Coverage

- `parser_test.go`: +8 tests for `TaskUpdatedMessage` parsing — terminal patch (`killed`), non-terminal (`running`), missing `patch` key, non-dict `patch` (string/slice/number/nil), `patch` without `status`, missing `task_id`/`uuid`/`session_id`, parametrized terminal statuses (`completed`/`failed`/`killed` plus `stopped` set membership), and `parseSystemMessage` dispatch yielding `*TaskUpdatedMessage` rather than a generic `*SystemMessage`.
- `version.go`: bumped `Version` from `"0.2.87"` to `"0.2.110"`.

## [0.2.87] - 2026-05-23

Port of Python SDK v0.1.77-v0.2.87 changes.

### Added

- **`EffortLevel` type alias and constants**: Added public `EffortLevel` type alias for effort string values with constants `EffortLevelLow`, `EffortLevelMedium`, `EffortLevelHigh`, `EffortLevelXHigh`, `EffortLevelMax`. Ported from Python SDK #951.
- **`Query.lastErrorResultText` error tracking**: When the CLI emits a result with `is_error=true` (e.g. `error_max_turns`, `error_during_execution`) and then exits non-zero, the trailing `ProcessError` is replaced with the structured error text from the result. Ported from Python SDK #918.
- **Stderr callback panic isolation**: The stderr read loop now recovers from panics in the user-provided `StderrCallback` per-line, so a failing callback does not silently drop every subsequent stderr line for the rest of the session. Ported from Python SDK #932.
- **`CancelledError` handling in mirror batcher**: The Go batcher already handled context cancellation gracefully via channel semantics; verified parity with Python SDK #931.

### Changed

- **`HookInput.PermissionSuggestions` type tightened**: Changed from `[]interface{}` to `[]map[string]interface{}` for type safety in both `internal/query.go` and `types.go`. Ported from Python SDK #955.
- **Error messages after error results are actionable**: Instead of the generic "command failed with exit code 1", consumers now receive messages like "Claude Code returned an error result: Reached maximum number of turns (60)".

### Test Coverage

- `types_test.go`: +2 tests for `EffortLevel` values and `HookInput.PermissionSuggestions` type.
- `internal/query_test.go`: +7 tests for ProcessError after error result (uses result error text, falls back to subtype, joins multiple errors, without result still surfaces, after success still surfaces, non-result message resets, session_state_changed does not reset).
- `internal/transport/subprocess_test.go`: +1 test for stderr callback panic isolation.
- `internal/callbacks_test.go`: +1 test for SimpleMirrorBatcher graceful close with pending items.
- Total: 640 tests passing across all packages.

## [0.1.76] - 2026-05-07

Port of Python SDK v0.1.73-v0.1.76 changes.

### Added

- **`DeferredToolUse`** struct and **`ResultMessage.DeferredToolUse`** field -- represents a tool use that was deferred by a PreToolUse hook returning decision "defer". The result message carries the deferred tool call so the caller can inspect it and decide whether to resume. Ported from Python SDK #865.
- **`ResultMessage.APIErrorStatus`** field (`*int`) -- surfaces the HTTP status code (e.g. 429, 500, 529) of the failing API call when `IsError` is true. Emitted by the CLI since v2.1.110. Safe to log (no message content). Ported from Python SDK #923.
- **`HookEventMessage`** type -- hook event emitted by the CLI when `IncludeHookEvents` is enabled. Arrives as `system` messages with subtype `hook_started` or `hook_response`. Implements the `Message` interface. Ported from Python SDK #917.
- **`ClaudeAgentOptions.IncludeHookEvents`** field -- when true, the CLI emits hook lifecycle events (PreToolUse, PostToolUse, Stop, etc.) into the message stream. Maps to the CLI's `--include-hook-events` flag. Ported from Python SDK #917.
- **`ClaudeAgentOptions.StrictMCPConfig`** field -- when true, only use MCP servers passed via `MCPServers`, ignoring all other MCP configurations (project `.mcp.json`, user/global settings, plugin-provided servers). Maps to the CLI's `--strict-mcp-config` flag. Ported from Python SDK #915.
- **`ToolPermissionContext`** new fields: `BlockedPath`, `DecisionReason`, `Title`, `DisplayName`, `Description` -- forwarded from the CLI's `can_use_tool` control request. The callback can now show users why they are being prompted and render richer permission prompts. Ported from Python SDK #909.
- **`permissionUpdateFromMap`** helper in internal/query.go -- deserializes `permission_suggestions` into proper `PermissionUpdate` instances with full field population (rules, behavior, mode, directories, destination). Previously suggestions were only partially deserialized (type only). Ported from Python SDK #920.
- **`HookOutput.Decision`** field documentation updated to include "defer" alongside "block".
- **`AgentDefinition.Effort`** and **`ClaudeAgentOptions.Effort`** comments updated to include "xhigh" (Opus 4.7 only; falls back to "high" on other models). Ported from Python SDK #914.
- Parser now handles `hook_started` and `hook_response` system message subtypes.
- Parser now extracts `deferred_tool_use` and `api_error_status` from result messages.
- Transport now passes `--strict-mcp-config` and `--include-hook-events` flags to the CLI.

### Test Coverage

- `parser_test.go`: +8 tests for `DeferredToolUse` parsing, `APIErrorStatus` parsing, `HookEventMessage` parsing.
- `types_test.go`: +5 tests for `DeferredToolUse` struct, `HookEventMessage` type, `ToolPermissionContext` new fields, `ResultMessage` new fields.
- `options_test.go`: +4 tests for `StrictMCPConfig` and `IncludeHookEvents` options.
- `internal/query_test.go`: +3 tests for permission context fields, `permissionUpdateFromMap` helper, and suggestion roundtrip.
- `internal/transport/subprocess_test.go`: +5 tests for `--strict-mcp-config`, `--include-hook-events`, and `--effort xhigh` flags.

## [0.1.73] - 2026-05-15

Port of Python SDK v0.1.65-v0.1.73 changes.

### Added

- **`SessionStoreFlushMode`** type alias and constants `SessionStoreFlushModeBatched` / `SessionStoreFlushModeEager` -- controls when transcript-mirror entries are flushed to the `SessionStore`. Ported from Python SDK #905.
- **`ClaudeAgentOptions.SessionStoreFlush`** field -- when `SessionStore` is set, controls the flush behaviour of the mirror batcher. Ported from Python SDK #905.
- **`SandboxNetworkConfig`** new fields: `AllowedDomains`, `DeniedDomains`, `AllowManagedDomainsOnly`, `AllowMachLookup`. Ported from Python SDK #893.
- **`session_resume.go`**: `sessionStoreWrapper`, `getProjectsDirFromEnv`, `buildMirrorBatcher` -- mirror batcher factory with proper projectsDir resolution. Ported from Python SDK #905.
- **Mirror batcher wiring in `client.go`**: `Connect()` now creates and wires `SimpleMirrorBatcher` when `SessionStore != nil`. `Close()` properly drains and closes the batcher. Fixes incomplete wiring from v0.1.65.

### Fixed

- **`parseSessionInfoFromLite`** (`sessions.go`): `created_at` is now extracted correctly when the first JSONL record lacks a timestamp field (e.g. `permission-mode` entries). Previously only the first line was scanned. Ported from Python SDK #904 / #907.


## [0.1.65] - 2026-05-01

Port of Python SDK v0.1.58–v0.1.65 changes.

### Added

- **`ErrNotImplemented`** sentinel error and **`BaseSessionStore`** struct — Go-specific additions for `SessionStore` implementors. Embed `BaseSessionStore` in a struct to get stub implementations for all interface methods that return `ErrNotImplemented`; override only the methods your adapter supports.
- **`ClaudeAgentOptions.LoadTimeoutMs`** — upper bound (in milliseconds, default 60 000) on `SessionStore.Load` / `ListSubkeys` calls during session resume materialisation. Prevents a slow store from blocking `Connect()` indefinitely.
- **`ListSessionsFromStore`** / **`GetSessionInfoFromStore`** / **`GetSessionMessagesFromStore`** — store-backed counterparts to the filesystem `ListSessions` / `GetSessionInfo` / `GetSessionMessages` functions. Ported from Python SDK v0.1.64 (#837).
- **`ListSubagentsFromStore`** / **`GetSubagentMessagesFromStore`** — store-backed counterparts to `ListSubagents` / `GetSubagentMessages`. Require the store to implement `ListSubkeys`. Ported from Python SDK v0.1.64 (#837).
- **`RenameSessionViaStore`** / **`TagSessionViaStore`** / **`DeleteSessionViaStore`** — store-backed counterparts to the filesystem `RenameSession` / `TagSession` / `DeleteSession` helpers. `DeleteSessionViaStore` silently skips WORM/append-only stores that return `ErrNotImplemented` from `Delete`. Ported from Python SDK v0.1.64 (#837).
- **`session_store_validation.go`**: `validateSessionStoreOptions` — validates `SessionStore`-related option combinations at `Connect()` time. Returns an error if `ContinueConversation` is set without an explicit `Resume` and the store does not implement `ListSessions` (required to resolve the latest session). Also forbids combining `EnableFileCheckpointing` with a store.
- **`session_resume.go`**: `MaterializedResume`, `materializeResumeSession`, `applyMaterializedOptions` — materialise a `SessionStore`-backed resume into a temporary `CLAUDE_CONFIG_DIR`, then clean it up after `Close()`. Supports both explicit `Resume` (UUID) and `ContinueConversation` (picks the most-recent non-sidechain session via `ListSessions`). Subagent transcripts are written via `ListSubkeys`/`Load`. Auth files are copied from the real config dir with the OAuth `refreshToken` field redacted. macOS Keychain credentials are read via `security find-generic-password` when `.credentials.json` is absent.
- **`client.go` session-store resume integration**: `Connect()` now calls `validateSessionStoreOptions` and, when a store + resume target is configured, calls `materializeResumeSession` before building the subprocess transport. `Close()` invokes the materialised resume's cleanup function to remove the temporary directory.
- **`ServerToolUseBlock`** and **`ServerToolResultBlock`** content block types, representing `server_tool_use` and `advisor_tool_result` content blocks respectively. Both implement the `ContentBlock` interface. `ParseContentBlock` now handles both types automatically.
- **`ServerToolName`** type alias (`string`) and constants: `ServerToolNameAdvisor`, `ServerToolNameWebSearch`, `ServerToolNameWebFetch`, `ServerToolNameCodeExecution`, `ServerToolNameBashCodeExecution`, `ServerToolNameTextEditorCodeExecution`, `ServerToolNameToolSearchRegex`, `ServerToolNameToolSearchBM25`.
- **`MirrorErrorMessage`** system message subtype — surfaces session-store errors from the CLI. Implements `Message`.
- **`SessionKey`**, **`SessionStoreEntry`**, **`SessionStoreListEntry`**, **`SessionSummaryEntry`**, **`SessionListSubkeysKey`** types for the session-store subsystem.
- **`SessionStore`** interface with `Append`, `Load`, `ListSessions`, `ListSessionSummaries`, `Delete`, `ListSubkeys` methods.
- **`ThinkingConfig.Display`** field — controls how thinking is displayed (e.g. `"summarized"`). Passed to the CLI as `--thinking-display <value>`.
- **`ClaudeAgentOptions.Skills`** field (`interface{}`) — accepts `nil`, `"all"`, or `[]string` of skill names. Controls which Claude Code Skills are enabled.
- **`ClaudeAgentOptions.SessionStore`** field — when set, `--session-mirror` is passed to the CLI; `transcript_mirror` frames from the CLI are routed to the store via `SimpleMirrorBatcher`.
- **`ListSubagents` / `GetSubagentMessages`** — read subagent transcripts stored in the sibling directory `<sessionID>/agent-<id>.jsonl`.
- **`session_store.go`**: `InMemorySessionStore` (thread-safe, in-process `SessionStore` implementation with summary sidecar), `ProjectKeyForDirectory`, `FilePathToSessionKey`.
- **`session_summary.go`**: `FoldSessionSummary` — incrementally updates a `SessionSummaryEntry` with set-once and last-wins field semantics.
- **`session_import.go`**: `ImportSessionToStore` — reads an existing session `.jsonl` file and appends all entries to a `SessionStore`.
- **`internal/mirror_batcher.go`**: `SimpleMirrorBatcher` — goroutine-based batcher that enqueues `transcript_mirror` frames and writes to a `SessionStore` with 3-retry logic and exponential backoff (200 ms / 800 ms between attempts). Adapters should dedupe by `entry["uuid"]` when present since a retried batch may partially overlap a prior partial write.
- **Parser**: `mirror_error` system message subtype now parsed into `*MirrorErrorMessage`.
- **Cascading session deletion**: `DeleteSession` now also removes the sibling subagent directory (same name without `.jsonl`) after deleting the main session file.

### Changed

- **Setting sources CLI flag format**: `--setting-sources` now uses `=` syntax (`--setting-sources=value`) instead of space-separated; empty slice emits `--setting-sources=` (flag is present, value is empty) — matches Python SDK 0.1.65 behaviour.
- **Skills in subprocess**: `applySkillsDefaults()` injects `Skill` (or `Skill(name)`) tools into `AllowedTools` and defaults `SettingSources` to `["user","project"]` when `Skills` is set and no explicit `SettingSources` was provided.
- **`PermissionModeDontAsk` documentation**: Corrected misleading comment — `dontAsk` denies unapproved tools (anything not pre-approved by allow rules); it does not bypass tool permission checks. Matches Python SDK v0.1.65 doc fix (#863).
- **`PermissionModeAuto` documentation**: Clarified that `auto` uses a model classifier to decide the appropriate permission level. Also corrected tab indentation in the constant block.

### Fixed

- **`ForkSessionViaStore` build error**: `ForkSessionViaStore` previously called an undefined helper `buildForkLines`, causing a compilation failure. The function has been rewritten with an inline implementation that loads entries from the store, filters sidechain entries, applies the optional `UpToMessageID` slice, remaps UUIDs, and writes the forked transcript to a new session key.
- **`SimpleMirrorBatcher` retry backoff**: Added 200 ms / 800 ms delays between retry attempts so transient store failures have time to resolve before the next attempt, matching Python SDK v0.1.65 behaviour (#857).

### Test Coverage

- **types_test.go**: +12 tests for new block types, constants, and structs.
- **parser_test.go**: +4 tests for `mirror_error`, `ServerToolUseBlock`, and `ServerToolResultBlock` parsing.
- **options_test.go**: +5 tests for `Skills` (all / list / nil), `SessionStore`, and `ThinkingConfig.Display`.
- **sessions_test.go**: +7 tests for `ListSubagents` and `GetSubagentMessages`.
- **session_mutations_test.go**: +1 test for cascading subagent directory deletion.
- **session_store_test.go** (new): 13 tests covering `InMemorySessionStore` conformance, `ProjectKeyForDirectory`, and `FilePathToSessionKey`.
- **session_summary_test.go** (new): 6 tests covering `FoldSessionSummary` semantics.
- **session_import_test.go** (new): 4 tests covering `ImportSessionToStore`.
- **types_base_store_test.go** (new): 3 tests for `BaseSessionStore` embedding and `ErrNotImplemented` sentinel.
- **session_store_validation_test.go** (new): 7 tests for `validateSessionStoreOptions` — nil options, no store, store without `ListSessions`, `ContinueConversation` requiring `ListSessions`, continue+resume skipping the check, full store, and `EnableFileCheckpointing` forbidden.
- **session_resume_test.go** (new): 12 tests for `applyMaterializedOptions` (3), `materializeResumeSession` (6), `isSafeSubpath`, and `writeJSONL`.
- **session_mutations_store_test.go** (new): 14 tests for `RenameSessionViaStore` (3), `TagSessionViaStore` (3), `DeleteSessionViaStore` (3), and `ForkSessionViaStore` (5).
- **sessions_store_test.go** (new): 24 tests for `ListSessionsFromStore` (6), `GetSessionInfoFromStore` (4), `GetSessionMessagesFromStore` (5), `ListSubagentsFromStore` (5), and `GetSubagentMessagesFromStore` (4).
- **internal/query_test.go**: +4 tests — `TestInitializeSendsSkillsListWhenSlice`, `TestInitializeOmitsSkillsForNil`, `TestInitializeOmitsSkillsForAll`, `TestTranscriptMirrorFramePeeled`.
- **internal/transport/subprocess_test.go**: +8 tests — thinking display forwarding, skills injection, `--session-mirror` flag.

## [0.1.57] - 2026-04-13

### Added

- **`PermissionModeAuto`**: Added `PermissionModeAuto = "auto"` constant to `PermissionMode` — ported from Python SDK v0.1.56 (#785). CLI v2.1.90+ and TypeScript SDK v0.2.91 both support `"auto"` mode; this is purely a type annotation addition.
- **`SystemPromptPreset.ExcludeDynamicSections`**: Added optional `ExcludeDynamicSections *bool` field to `SystemPromptPreset` — ported from Python SDK v0.1.57 (#797). When set, passes `excludeDynamicSections` in the `initialize` control message so the CLI strips per-user dynamic sections (working directory, auto-memory, git status) from the preset system prompt and re-injects them into the first user message, keeping the system prompt byte-identical across users for cross-user prompt-cache hits. Older CLIs silently ignore the field.
- **MCP large output test file**: Added `internal/transport/mcp_large_output_test.go` documenting the two-layer CLI spill mechanism and confirming SDK env-var handling — ported from Python SDK `test_mcp_large_output.py`. Tests cover `MAX_MCP_OUTPUT_TOKENS` passthrough, `CLAUDECODE` stripping, `CLAUDE_AGENT_SDK_VERSION` invariants, and layer-2 threshold boundary documentation (#756)

### Changed

- **`AgentDefinition.Effort` supports integer values**: Changed `Effort` field type from `string` to `interface{}` so it now accepts both string literals (`"low"`, `"medium"`, `"high"`, `"max"`) and numeric integer effort budgets — aligned with Python SDK where `effort: Literal[...] | int | None` (#782)
- **Thinking flags**: Fixed thinking config CLI flag generation — ported from Python SDK v0.1.57 (#796):
  - `adaptive` → `--thinking adaptive` (was `--thinking-mode adaptive`)
  - `enabled` → `--max-thinking-tokens <budget_tokens>` (was `--thinking-mode enabled` + `--thinking-budget-tokens`)
  - `disabled` → `--thinking disabled` (was `--thinking-mode disabled`)
  - Deprecated `max_thinking_tokens` only emitted when `thinking` is unset

### Test Coverage

- **types_test.go**: `TestPermissionModeAllConstants` updated to include `PermissionModeAuto`
- **options_test.go**: +2 tests — `TestOptionsWithPermissionMode` (auto case), `TestOptionsWithSystemPromptPresetAndExcludeDynamicSections`
- **internal/query_test.go**: +2 tests — `TestQueryInitializeSendsExcludeDynamicSections`, `TestQueryInitializeOmitsExcludeDynamicSectionsWhenUnset`
- **internal/transport/subprocess_extended_test.go**: Updated "thinking config" test case and added `TestBuildCommand_ThinkingPrecedence`; new parametrized tests for adaptive/enabled/disabled thinking types with absence assertions
- **types_test.go**: +2 tests — `TestAgentDefinition_EffortAsInt`, `TestVersion`
- **internal/transport/subprocess_test.go**: +3 tests — `TestSDKVersionAlwaysSet`, `TestSDKVersionNotOverridableByUserEnv`, `TestMAXMCPOutputTokensPassthrough`
- **internal/transport/mcp_large_output_test.go**: +11 tests — `TestLayer1*` (3), `TestEnvInheritedFromOSEnviron`, `TestOptionsEnvOverridesOSEnviron`, `TestCLAUDECODEStrippedInMCPTest`, `TestSDKManagedVarsAlwaysSet`, `TestSDKVersionCannotBeOverriddenByUserEnvInMCPTest`, `TestLayer2*` (3)
- Total: 351 tests passing across all packages

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

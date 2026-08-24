package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file is the Go counterpart of Python's sdk_mcp_bridge.py plus the
// tool-execution semantics of create_sdk_mcp_server's run_tool (see
// anthropic-ai/claude-agent-sdk-python#1218). Like Python, SDK MCP servers
// are real MCP servers — *mcp.Server from modelcontextprotocol/go-sdk —
// served over that SDK's in-memory transport. The bridge connects the CLI's
// mcp_message control frames to a raw connection on the client end of
// mcp.NewInMemoryTransports, so every method the server implements
// (initialize, tools, resources, prompts, ping, ...) is dispatched by the
// go-sdk itself rather than re-implemented here:
//
//   - One session per server, started lazily by the first message: a
//     *mcp.ServerSession on one end of the pipe pair and a reader goroutine
//     on the other. Responses resolve pending requests by JSON-RPC id
//     (unknown ids are dropped with a debug log); server-initiated requests
//     are answered with {} for ping and refused with -32601 otherwise;
//     server-initiated notifications are dropped.
//   - The factory-built server (CreateSdkMcpServer) keeps the historical
//     wire semantics via a receiving middleware: initialize echoes the
//     client's protocolVersion with capabilities
//     {"experimental": {}, "tools": {"listChanged": false}}; tools/list uses
//     the camelCase wire format with maxResultSizeChars in _meta under
//     "anthropic/maxResultSizeChars"; tools/call owns the factory semantics
//     (unknown tools, schema-invalid arguments, handler errors, panics and
//     malformed payloads come back as isError results; success always
//     carries "isError": false); resources/* and prompts/* are refused with
//     -32601, matching what the Python factory server answers. go-sdk's own
//     result marshaling cannot express these (isError is omitempty, tool
//     annotations always carry the hint defaults), so the middleware renders
//     the payloads itself.
//   - Hand-built servers (MCPSdkServerConfig.Instance set directly) are
//     served untouched: resources, prompts and custom methods reach the CLI
//     with whatever semantics the go-sdk gives them.
//   - A notifications/cancelled settles the in-flight waiter with a -32800
//     "Request cancelled" error and is also forwarded so the go-sdk cancels
//     the handler's context. Factory handlers ignore the context and run to
//     completion; the late response arrives after the waiter is gone and is
//     dropped (the same caveat Python documents for hand-built servers).
//   - A request that reuses an id still in flight is refused with a JSON-RPC
//     error instead of reaching the server.
//   - go-sdk rejects a second initialize on a live session ("duplicate
//     initialize"), but the CLI re-initializes SDK servers at turn starts,
//     so the bridge answers a repeated initialize from the first handshake's
//     result without touching the server.
//   - If the server session ends underneath the CLI (the server stopped),
//     every non-initialize message is answered with an error naming the
//     server and the cause; a new initialize starts a fresh session.
//
// Notifications (no valid JSON-RPC id) get no reply; Handle returns nil for
// them and the control request that carried one is acked by the caller.

const (
	// JSON-RPC "method not found": what a client answers for a request it
	// does not support, and what a tools-only factory server answers for
	// resources/* and prompts/*.
	mcpMethodNotFound = -32601
	// JSON-RPC "internal error": malformed messages, request-id reuse and
	// bridge lifecycle errors (closed/stopped server).
	mcpInternalError = -32603
	// JSON-RPC "request cancelled": settles a request the client abandoned
	// via notifications/cancelled (what mcp 2's own HTTP transport answers).
	mcpRequestCancelled = -32800
)

// defaultMCPProtocolVersion is reported when the client's initialize does not
// name a protocolVersion. Matches the version the SDK has always advertised.
const defaultMCPProtocolVersion = "2024-11-05"

// mcpShutdownGrace bounds how long closing a session waits for the server to
// wind down after the connection is closed. Mirrors Python's
// SHUTDOWN_GRACE_SECONDS: servers stop within milliseconds; only a handler
// that does not react to cancellation can hold one open, and that is not
// worth hanging a reconnect or shutdown on. A var (like Python's module
// constant) so tests can shrink it the way the Python suite monkeypatches
// SHUTDOWN_GRACE_SECONDS.
var mcpShutdownGrace = 5 * time.Second

// MCPServer is the factory tool spec CreateSdkMcpServer builds from: a name,
// a version and the tools served under the factory wire semantics.
type MCPServer struct {
	Name    string
	Version string
	Tools   []MCPTool
}

// MCPTool represents an MCP tool.
type MCPTool struct {
	Name        string
	Description string
	InputSchema interface{}
	Annotations interface{}
	Handler     func(args map[string]interface{}) (map[string]interface{}, error)
}

// ToolAnnotations represents hints for tool usage.
type ToolAnnotations struct {
	ReadOnlyHint       *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint    *bool `json:"destructiveHint,omitempty"`
	IdempotentHint     *bool `json:"idempotentHint,omitempty"`
	OpenWorldHint      *bool `json:"openWorldHint,omitempty"`
	MaxResultSizeChars *int  `json:"maxResultSizeChars,omitempty"`
}

// pendingMCPRequest is the slot an in-flight request's response is delivered
// to. It lives in the session's table of unanswered requests until the server
// answers it or a cancellation settles it; the first settle wins. rawID is
// the request id exactly as it arrived, echoed back in the response (the
// normalized form used as the table key may differ in Go type).
type pendingMCPRequest struct {
	rawID    interface{}
	done     chan struct{}
	mu       sync.Mutex
	response map[string]interface{}
	settled  bool
}

// settle delivers the response and unblocks the waiter. It returns false
// when the request was already settled (e.g. by a cancellation), in which
// case the late response is discarded.
func (p *pendingMCPRequest) settle(response map[string]interface{}) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.settled {
		return false
	}
	p.settled = true
	p.response = response
	close(p.done)
	return true
}

// wait blocks until the request is settled and returns its response.
func (p *pendingMCPRequest) wait() map[string]interface{} {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.response
}

// normalizeRequestID canonicalizes a JSON-RPC request id. mcp accepts string
// and integer ids only; anything else (a fractional number, null, a bool)
// makes the frame a notification that gets no reply. Integral floats are
// canonicalized to int64 so an id matches its notifications/cancelled
// requestId regardless of which Go numeric type each arrived as.
func normalizeRequestID(v interface{}) (interface{}, bool) {
	switch id := v.(type) {
	case string:
		return id, true
	case int:
		return int64(id), true
	case int64:
		return id, true
	case float64:
		if !math.IsNaN(id) && !math.IsInf(id, 0) && id == math.Trunc(id) {
			return int64(id), true
		}
	}
	return nil, false
}

// SDKMCPBridge routes raw JSON-RPC messages from the CLI to one in-process
// SDK MCP server through the go-sdk's in-memory transport. One bridge exists
// per configured SDK server for the lifetime of a Query and runs one session
// at a time; it must be closed with the Query.
type SDKMCPBridge struct {
	name    string
	server  *mcp.Server
	mu      sync.Mutex
	session *sdkMCPSession
	closed  bool
}

// NewSDKMCPBridge creates a bridge for one server instance. The session is
// started lazily by the first message.
func NewSDKMCPBridge(name string, server *mcp.Server) *SDKMCPBridge {
	return &SDKMCPBridge{name: name, server: server}
}

// Handle processes one JSON-RPC message from the CLI. It returns the
// JSON-RPC response for requests, or nil for notifications and responses,
// which expect no reply.
func (b *SDKMCPBridge) Handle(message map[string]interface{}) map[string]interface{} {
	method, _ := message["method"].(string)
	rawID := message["id"]
	id, validID := normalizeRequestID(rawID)

	if method == "" {
		if validID {
			// A frame with an id but no method is a response when it carries
			// result/error (dropped: nothing here awaits server answers), and
			// malformed otherwise.
			if _, isResponse := message["result"]; isResponse {
				return nil
			}
			if _, isResponse := message["error"]; isResponse {
				return nil
			}
			return mcpErrorResponse(rawID, mcpInternalError, "Invalid JSON-RPC message: missing method")
		}
		return nil
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		if validID {
			return mcpErrorResponse(rawID, mcpInternalError, fmt.Sprintf("SDK MCP server %q is closed", b.name))
		}
		return nil
	}
	session := b.session
	switch {
	case session == nil:
		session = newSDKMCPSession(b.name, b.server)
		b.session = session
		b.mu.Unlock()
	case session.finished():
		// The server stopped underneath the CLI. It stays stopped, with every
		// message answered by an error saying so, until the CLI starts it
		// again with a new handshake (which it does for a server it considers
		// failed).
		if method != "initialize" {
			b.mu.Unlock()
			if validID {
				return mcpErrorResponse(rawID, mcpInternalError,
					fmt.Sprintf("SDK MCP server %q stopped: %s", b.name, session.failureCause()))
			}
			return nil
		}
		stopped := session
		session = newSDKMCPSession(b.name, b.server)
		b.session = session
		b.mu.Unlock()
		stopped.close()
	default:
		b.mu.Unlock()
	}

	return session.handle(message, id, rawID, validID)
}

// Close stops the session, if any, waiting (boundedly) for it to wind down.
// Safe to call more than once.
func (b *SDKMCPBridge) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	session := b.session
	b.session = nil
	b.mu.Unlock()
	if session != nil {
		session.close()
	}
}

// sdkMCPSession is one *mcp.Server session over one in-memory transport pair:
// the go-sdk serves the server end, and the bridge owns the client end's raw
// connection and its reader goroutine. Responses are matched to waiting
// requests by JSON-RPC id.
type sdkMCPSession struct {
	name       string
	ctx        context.Context
	cancel     context.CancelFunc
	conn       mcp.Connection
	serverSess *mcp.ServerSession

	// done is closed when the reader goroutine ends and the server session
	// has wound down: from then on nothing sent to this session can be
	// answered any more.
	done chan struct{}

	mu         sync.Mutex
	pending    map[interface{}]*pendingMCPRequest
	failure    string
	initResult map[string]interface{}
}

// newSDKMCPSession connects the server to one end of an in-memory transport
// pair and starts the reader goroutine on the other end.
func newSDKMCPSession(name string, server *mcp.Server) *sdkMCPSession {
	ctx, cancel := context.WithCancel(context.Background())
	session := &sdkMCPSession{
		name:    name,
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
		pending: make(map[interface{}]*pendingMCPRequest),
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSess, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		cancel()
		session.failure = err.Error()
		close(session.done)
		return session
	}
	session.serverSess = serverSess
	conn, err := clientTransport.Connect(ctx)
	if err != nil {
		cancel()
		session.failure = err.Error()
		close(session.done)
		return session
	}
	session.conn = conn

	go func() {
		session.readLoop()
		// Let the server session wind down on its own once its input is
		// gone. A handler that ignores cancellation can hold it open; close()
		// stops waiting after mcpShutdownGrace.
		serverSess.Wait()
		close(session.done)
	}()
	return session
}

// finished reports whether nothing sent to this session can be answered any
// more.
func (s *sdkMCPSession) finished() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// failureCause names why the session ended, for the stopped-server error.
func (s *sdkMCPSession) failureCause() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != "" {
		return s.failure
	}
	return "its session ended"
}

// readLoop drains the server end of the pipe: responses resolve pending
// requests, server-initiated requests are answered from a goroutine (the
// reader must never wait on the server it drains), and notifications from
// the server are dropped.
func (s *sdkMCPSession) readLoop() {
	for {
		msg, err := s.conn.Read(s.ctx)
		if err != nil {
			s.mu.Lock()
			if s.failure == "" && s.ctx.Err() == nil {
				s.failure = err.Error()
			}
			s.mu.Unlock()
			s.failPending("session closed")
			return
		}
		switch m := msg.(type) {
		case *jsonrpc.Response:
			s.respond(m)
		case *jsonrpc.Request:
			if m.IsCall() {
				go s.answerServerRequest(m)
			} else {
				slog.Debug("Dropping notification from SDK MCP server", "server", s.name, "method", m.Method)
			}
		}
	}
}

// respond delivers a response from the server to its waiting request.
func (s *sdkMCPSession) respond(m *jsonrpc.Response) {
	id, ok := normalizeRequestID(m.ID.Raw())
	if !ok {
		slog.Debug("Dropping response with invalid id from SDK MCP server", "server", s.name)
		return
	}
	s.mu.Lock()
	pending := s.pending[id]
	if pending != nil {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	if pending == nil {
		slog.Debug("Dropping response for unknown request id from SDK MCP server", "server", s.name, "id", m.ID.Raw())
		return
	}
	data, err := jsonrpc.EncodeMessage(m)
	if err != nil {
		pending.settle(mcpErrorResponse(pending.rawID, mcpInternalError, fmt.Sprintf("Invalid response from server: %v", err)))
		return
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		pending.settle(mcpErrorResponse(pending.rawID, mcpInternalError, fmt.Sprintf("Invalid response from server: %v", err)))
		return
	}
	// Echo the id exactly as the request carried it, and restore the concrete
	// slice types the hand-rolled bridge used to produce.
	payload["id"] = pending.rawID
	pending.settle(normalizeJSONValue(payload).(map[string]interface{}))
}

// answerServerRequest answers a request the server sent to the client. A
// ping is answered with an empty result; nothing carries other requests
// (roots, sampling, elicitation) to the CLI, so they are refused with
// "method not found", which is what a client that does not support them
// answers, and the server's caller fails at once instead of waiting.
func (s *sdkMCPSession) answerServerRequest(request *jsonrpc.Request) {
	var response *jsonrpc.Response
	if request.Method == "ping" {
		response = &jsonrpc.Response{ID: request.ID, Result: json.RawMessage(`{}`)}
	} else {
		slog.Warn("SDK MCP server sent a request to the client; requests to "+
			"the client are not supported for SDK servers yet, refusing it",
			"server", s.name, "method", request.Method)
		response = &jsonrpc.Response{ID: request.ID, Error: &jsonrpc.Error{
			Code:    mcpMethodNotFound,
			Message: fmt.Sprintf("%s is not supported for SDK servers", request.Method),
		}}
	}
	// A closed connection means the session is shutting down and the server's
	// caller is being cancelled anyway.
	_ = s.conn.Write(s.ctx, response)
}

// failPending settles every unanswered request with an error naming the
// session's end.
func (s *sdkMCPSession) failPending(reason string) {
	s.mu.Lock()
	pending := make([]*pendingMCPRequest, 0, len(s.pending))
	for _, p := range s.pending {
		pending = append(pending, p)
	}
	s.pending = make(map[interface{}]*pendingMCPRequest)
	s.mu.Unlock()
	for _, p := range pending {
		p.settle(mcpErrorResponse(p.rawID, mcpInternalError, fmt.Sprintf("SDK MCP server %q %s", s.name, reason)))
	}
}

// handle forwards one message (known to carry a method) to the server and,
// for requests, waits for the response.
func (s *sdkMCPSession) handle(message map[string]interface{}, id, rawID interface{}, validID bool) map[string]interface{} {
	method := message["method"].(string)

	if method == "initialize" && validID {
		// go-sdk rejects a second initialize on a live session, but the CLI
		// re-initializes SDK servers at turn starts: answer from the first
		// handshake's result and leave the session (and its in-flight calls)
		// untouched.
		if cached := s.cachedInitializeResult(); cached != nil {
			return mcpResultResponse(rawID, cached)
		}
		message = withDefaultInitializeParams(message)
	}

	if method == "notifications/cancelled" {
		s.cancelPending(message["params"])
		// The notification is also forwarded so the go-sdk cancels the
		// handler's context — but only when it names a valid request id:
		// go-sdk coerces a fractional id to an integer (MakeID), which could
		// cancel an unrelated in-flight request. Factory handlers ignore
		// their context and run to completion; their late responses are
		// dropped as unknown ids.
		params, _ := message["params"].(map[string]interface{})
		if _, ok := normalizeRequestID(params["requestId"]); !ok {
			return nil
		}
	}

	if !validID {
		// A notification — or a frame whose id is not a valid JSON-RPC id,
		// which mcp reads as a notification. It is forwarded with the invalid
		// id stripped and gets no reply.
		msg, err := encodeMCPMessage(message, !validID)
		if err != nil {
			slog.Debug("Dropping unencodable notification for SDK MCP server", "server", s.name, "error", err)
			return nil
		}
		// No reply is due whether or not the write lands.
		_ = s.conn.Write(s.ctx, msg)
		return nil
	}

	// A request. Register its id before dispatching so a second request
	// reusing the id is refused without reaching the server.
	pending := &pendingMCPRequest{rawID: rawID, done: make(chan struct{})}
	s.mu.Lock()
	if _, exists := s.pending[id]; exists {
		s.mu.Unlock()
		return mcpErrorResponse(rawID, mcpInternalError, fmt.Sprintf("Request id %v is already in flight", id))
	}
	s.pending[id] = pending
	s.mu.Unlock()

	msg, err := encodeMCPMessage(message, false)
	if err != nil {
		s.removePending(id, pending)
		return mcpErrorResponse(rawID, mcpInternalError, fmt.Sprintf("Invalid JSON-RPC message: %v", err))
	}
	if err := s.conn.Write(s.ctx, msg); err != nil {
		// The session went away between being chosen and this send.
		s.removePending(id, pending)
		return mcpErrorResponse(rawID, mcpInternalError, fmt.Sprintf("SDK MCP server %q session closed", s.name))
	}

	response := pending.wait()
	if method == "initialize" {
		if result, ok := response["result"].(map[string]interface{}); ok {
			s.mu.Lock()
			s.initResult = result
			s.mu.Unlock()
		}
	}
	return response
}

// removePending releases a request id, unless it was since reused by a newer
// request (possible after a cancellation).
func (s *sdkMCPSession) removePending(id interface{}, pending *pendingMCPRequest) {
	s.mu.Lock()
	if s.pending[id] == pending {
		delete(s.pending, id)
	}
	s.mu.Unlock()
}

// cancelPending settles an in-flight request named by a
// notifications/cancelled message with a "Request cancelled" error. The
// running handler is not interrupted (factory handlers ignore their
// context); its late result is discarded by settle.
func (s *sdkMCPSession) cancelPending(paramsRaw interface{}) {
	params, _ := paramsRaw.(map[string]interface{})
	if params == nil {
		return
	}
	id, ok := normalizeRequestID(params["requestId"])
	if !ok {
		return
	}
	s.mu.Lock()
	pending, exists := s.pending[id]
	if exists {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	if exists {
		pending.settle(mcpErrorResponse(pending.rawID, mcpRequestCancelled, "Request cancelled"))
	}
}

// cachedInitializeResult returns the first handshake's result, used to
// answer a repeated initialize on a live session.
func (s *sdkMCPSession) cachedInitializeResult() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initResult
}

// close ends the session and waits (boundedly) for it to wind down. Closing
// the connection is the stop signal: the go-sdk cancels what it has in
// flight, the session ends and the reader goroutine returns. A handler that
// ignores cancellation can hold that up; past the grace period the session
// is abandoned rather than blocking the caller on it.
func (s *sdkMCPSession) close() {
	s.cancel()
	if s.conn != nil {
		_ = s.conn.Close()
	}
	select {
	case <-s.done:
	case <-time.After(mcpShutdownGrace):
		slog.Warn("SDK MCP server did not stop within the shutdown grace period "+
			"(a handler is probably blocked); no longer waiting for it",
			"server", s.name, "grace", mcpShutdownGrace)
		s.failPending("session closed")
	}
}

// encodeMCPMessage renders a raw JSON-RPC message map into the go-sdk's
// message type for writing to the connection. With stripID, the id key is
// removed first (used to forward frames whose id is not a valid JSON-RPC id
// as plain notifications).
func encodeMCPMessage(message map[string]interface{}, stripID bool) (jsonrpc.Message, error) {
	if stripID {
		if _, hasID := message["id"]; hasID {
			copy := make(map[string]interface{}, len(message))
			for key, value := range message {
				if key != "id" {
					copy[key] = value
				}
			}
			message = copy
		}
	}
	data, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	return jsonrpc.DecodeMessage(data)
}

// withDefaultInitializeParams returns the initialize message with a params
// object that names a protocolVersion, defaulting it when the client did not
// (the historical fallback the SDK has always advertised).
func withDefaultInitializeParams(message map[string]interface{}) map[string]interface{} {
	params, _ := message["params"].(map[string]interface{})
	if version, _ := params["protocolVersion"].(string); version != "" {
		return message
	}
	out := make(map[string]interface{}, len(message)+1)
	for key, value := range message {
		out[key] = value
	}
	merged := make(map[string]interface{}, len(params)+1)
	for key, value := range params {
		merged[key] = value
	}
	merged["protocolVersion"] = defaultMCPProtocolVersion
	out["params"] = merged
	return out
}

// normalizeJSONValue restores the concrete container and number types the
// hand-rolled bridge produced: JSON objects stay maps, arrays whose elements
// are all objects become []map[string]interface{} (what the wire tests
// assert on), and integral numbers become int. JSON round-trips make every
// number a float64, but re-marshaling renders an integral float identically
// (json.Marshal(float64(4)) and json.Marshal(int(4)) both produce "4"), so
// the wire form is unchanged.
func normalizeJSONValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, element := range v {
			v[key] = normalizeJSONValue(element)
		}
		return v
	case []interface{}:
		allMaps := true
		for _, element := range v {
			if _, ok := element.(map[string]interface{}); !ok {
				allMaps = false
				break
			}
		}
		if allMaps {
			out := make([]map[string]interface{}, len(v))
			for i, element := range v {
				out[i] = normalizeJSONValue(element).(map[string]interface{})
			}
			return out
		}
		for i, element := range v {
			v[i] = normalizeJSONValue(element)
		}
		return v
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) && v == math.Trunc(v) && v >= math.MinInt64 && v <= math.MaxInt64 {
			return int(v)
		}
		return v
	default:
		return value
	}
}

// --- Factory server ------------------------------------------------------------

// rawJSONResult is an mcp.Result rendered from a raw payload map, so the
// factory server's wire format does not depend on the go-sdk's own result
// marshaling (which cannot express it: isError is omitempty and tool
// annotations always carry the hint defaults).
type rawJSONResult struct {
	mcp.ResultBase
	payload map[string]interface{}
}

// MarshalJSON implements json.Marshaler.
func (r *rawJSONResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.payload)
}

// BuildToolServer builds the factory *mcp.Server for a name, a version and a
// set of tools: the server CreateSdkMcpServer serves under the historical
// factory wire semantics. A receiving middleware owns the methods whose wire
// format the SDK pins down (initialize capabilities, tools/list, tools/call,
// and the -32601 refusal of resources/* and prompts/* a tools-only server
// answers); everything else is dispatched by the go-sdk itself.
func BuildToolServer(name, version string, tools []MCPTool) *mcp.Server {
	spec := &MCPServer{Name: name, Version: version, Tools: tools}
	server := mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Experimental: map[string]interface{}{},
			Tools:        &mcp.ToolCapabilities{ListChanged: false},
		},
	})
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch {
			case method == "initialize":
				// Let the go-sdk run the handshake (it owns the session
				// state), then render the result in the pinned wire format:
				// go-sdk's marshaling drops "experimental": {} and
				// "listChanged": false via omitempty.
				result, err := next(ctx, method, req)
				if err != nil {
					return nil, err
				}
				initResult, _ := result.(*mcp.InitializeResult)
				protocolVersion := defaultMCPProtocolVersion
				if initResult != nil && initResult.ProtocolVersion != "" {
					protocolVersion = initResult.ProtocolVersion
				}
				return &rawJSONResult{payload: map[string]interface{}{
					"protocolVersion": protocolVersion,
					"capabilities": map[string]interface{}{
						"experimental": map[string]interface{}{},
						"tools":        map[string]interface{}{"listChanged": false},
					},
					"serverInfo": map[string]interface{}{
						"name":    spec.Name,
						"version": spec.Version,
					},
				}}, nil
			case method == "tools/list":
				return &rawJSONResult{payload: map[string]interface{}{"tools": spec.listTools()}}, nil
			case method == "tools/call":
				params, _ := req.GetParams().(*mcp.CallToolParamsRaw)
				var toolName string
				var arguments map[string]interface{}
				if params != nil {
					toolName = params.Name
					if len(params.Arguments) > 0 {
						// An arguments value that is not a JSON object
						// unmarshals to nil, like the old type assertion.
						_ = json.Unmarshal(params.Arguments, &arguments)
					}
				}
				return &rawJSONResult{payload: spec.runTool(toolName, arguments)}, nil
			case strings.HasPrefix(method, "resources/") || strings.HasPrefix(method, "prompts/"):
				// A tools-only factory server does not implement these,
				// exactly what the mcp library answers for them.
				return nil, &jsonrpc.Error{
					Code:    mcpMethodNotFound,
					Message: fmt.Sprintf("Method '%s' not found", method),
				}
			default:
				return next(ctx, method, req)
			}
		}
	})
	return server
}

// listTools renders the tools in the camelCase wire format the CLI expects.
func (s *MCPServer) listTools() []map[string]interface{} {
	tools := make([]map[string]interface{}, 0, len(s.Tools))
	for _, tool := range s.Tools {
		inputSchema := tool.InputSchema
		if inputSchema == nil {
			inputSchema = map[string]interface{}{}
		} else if _, ok := inputSchema.(map[string]interface{}); !ok {
			// A schema the factory cannot interpret lists as an empty object
			// schema, mirroring the Python factory's plain-class case
			// (_build_input_schema: anything that is neither a schema dict
			// nor a TypedDict becomes {"type": "object", "properties": {}}).
			inputSchema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}
		wire := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": inputSchema,
		}
		annotations, maxResultSizeChars := toolAnnotationsWire(tool.Annotations)
		if annotations != nil {
			wire["annotations"] = annotations
		}
		// maxResultSizeChars travels in _meta under a namespaced key because
		// MCP clients drop annotation fields they do not know (#756); it
		// never appears inside the wire annotations.
		if maxResultSizeChars != nil {
			wire["_meta"] = map[string]interface{}{
				"anthropic/maxResultSizeChars": maxResultSizeChars,
			}
		}
		tools = append(tools, wire)
	}
	return tools
}

// toolAnnotationHints are the standard MCP tool-annotation hints, accepted
// in camelCase (the wire spelling) or snake_case, camelCase winning when
// both are given.
var toolAnnotationHints = []struct{ camel, snake string }{
	{"readOnlyHint", "read_only_hint"},
	{"destructiveHint", "destructive_hint"},
	{"idempotentHint", "idempotent_hint"},
	{"openWorldHint", "open_world_hint"},
}

// toolAnnotationsWire normalizes a tool's annotations to the camelCase wire
// form and extracts maxResultSizeChars (either spelling) for _meta. It
// accepts the ToolAnnotations struct (value or pointer) or a plain map using
// either hint spelling. A nil value yields no annotations.
func toolAnnotationsWire(annotations interface{}) (map[string]interface{}, interface{}) {
	var wire map[string]interface{}
	var maxResultSizeChars interface{}
	set := func(key string, value interface{}) {
		if wire == nil {
			wire = make(map[string]interface{})
		}
		wire[key] = value
	}

	switch ann := annotations.(type) {
	case nil:
		return nil, nil
	case *ToolAnnotations:
		if ann == nil {
			return nil, nil
		}
		return toolAnnotationsWire(*ann)
	case ToolAnnotations:
		if ann.ReadOnlyHint != nil {
			set("readOnlyHint", *ann.ReadOnlyHint)
		}
		if ann.DestructiveHint != nil {
			set("destructiveHint", *ann.DestructiveHint)
		}
		if ann.IdempotentHint != nil {
			set("idempotentHint", *ann.IdempotentHint)
		}
		if ann.OpenWorldHint != nil {
			set("openWorldHint", *ann.OpenWorldHint)
		}
		if ann.MaxResultSizeChars != nil {
			maxResultSizeChars = *ann.MaxResultSizeChars
		}
		if wire == nil {
			wire = make(map[string]interface{})
		}
	case map[string]interface{}:
		consumed := make(map[string]bool, 2*len(toolAnnotationHints)+2)
		for _, hint := range toolAnnotationHints {
			consumed[hint.camel] = true
			consumed[hint.snake] = true
			if value, ok := ann[hint.camel]; ok {
				set(hint.camel, value)
			} else if value, ok := ann[hint.snake]; ok {
				set(hint.camel, value)
			}
		}
		consumed["maxResultSizeChars"] = true
		consumed["max_result_size_chars"] = true
		if value, ok := ann["maxResultSizeChars"]; ok {
			maxResultSizeChars = value
		} else if value, ok := ann["max_result_size_chars"]; ok {
			maxResultSizeChars = value
		}
		// Unknown keys pass through untouched, like extras on the Python
		// annotations model.
		for key, value := range ann {
			if !consumed[key] {
				set(key, value)
			}
		}
	default:
		// An annotations value of any other type goes on the wire as-is.
		if m, ok := ann.(map[string]string); ok {
			for key, value := range m {
				set(key, value)
			}
		}
	}
	return wire, maxResultSizeChars
}

// runTool owns the factory tool semantics, mirroring Python's run_tool:
// unknown tools, schema-invalid arguments, handler errors (including panics)
// and malformed handler payloads all come back as isError results, and
// success results always carry "isError": false.
func (s *MCPServer) runTool(toolName string, arguments map[string]interface{}) (result map[string]interface{}) {
	var tool *MCPTool
	for i := range s.Tools {
		if s.Tools[i].Name == toolName {
			tool = &s.Tools[i]
			break
		}
	}
	if tool == nil {
		return mcpToolErrorResult(fmt.Sprintf("Tool '%s' not found", toolName))
	}
	if message := validateToolArguments(tool.InputSchema, arguments); message != "" {
		return mcpToolErrorResult("Input validation error: " + message)
	}
	defer func() {
		if r := recover(); r != nil {
			result = mcpToolErrorResult(fmt.Sprintf("%v", r))
		}
	}()
	handlerResult, err := tool.Handler(arguments)
	if err != nil {
		return mcpToolErrorResult(err.Error())
	}
	return shapeToolResult(handlerResult)
}

// mcpToolErrorResult builds an isError tool result carrying a text message.
func mcpToolErrorResult(message string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": message},
		},
		"isError": true,
	}
}

// shapeToolResult converts a handler's result dict to the wire result:
// content blocks converted, snake_case "is_error" mapped to "isError", and
// nothing else carried over.
func shapeToolResult(result map[string]interface{}) map[string]interface{} {
	content, convErr := convertToolContent(result["content"])
	if convErr != "" {
		return mcpToolErrorResult(convErr)
	}
	isError, _ := result["is_error"].(bool)
	return map[string]interface{}{
		"content": content,
		"isError": isError,
	}
}

// convertToolContent maps the content of a tool handler's result dict to MCP
// content blocks, mirroring Python's _convert_tool_content: text and image
// blocks pass through, resource links and text resources are flattened to
// text (what the CLI renders), and binary resources and unknown block types
// are dropped with a warning. A missing required field is an error, returned
// as the message Python's KeyError would produce ("'text'", "'data'", ...).
func convertToolContent(raw interface{}) ([]map[string]interface{}, string) {
	if raw == nil {
		return []map[string]interface{}{}, ""
	}
	var items []interface{}
	switch content := raw.(type) {
	case []interface{}:
		items = content
	case []map[string]interface{}:
		items = make([]interface{}, len(content))
		for i, item := range content {
			items[i] = item
		}
	default:
		return nil, fmt.Sprintf("'content' must be a list, got %T", raw)
	}

	converted := make([]map[string]interface{}, 0, len(items))
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			return nil, fmt.Sprintf("content item must be an object, got %T", rawItem)
		}
		itemType, _ := item["type"].(string)
		switch itemType {
		case "text":
			text, ok := item["text"].(string)
			if !ok {
				return nil, "'text'"
			}
			converted = append(converted, map[string]interface{}{"type": "text", "text": text})
		case "image":
			data, ok := item["data"].(string)
			if !ok {
				return nil, "'data'"
			}
			mimeType, ok := item["mimeType"].(string)
			if !ok {
				return nil, "'mimeType'"
			}
			converted = append(converted, map[string]interface{}{
				"type": "image", "data": data, "mimeType": mimeType,
			})
		case "resource_link":
			var parts []string
			for _, key := range []string{"name", "uri", "description"} {
				if value, ok := item[key]; ok && value != nil && value != "" {
					parts = append(parts, fmt.Sprintf("%v", value))
				}
			}
			text := strings.Join(parts, "\n")
			if text == "" {
				text = "Resource link"
			}
			converted = append(converted, map[string]interface{}{"type": "text", "text": text})
		case "resource":
			resource, _ := item["resource"].(map[string]interface{})
			if text, ok := resource["text"].(string); ok {
				converted = append(converted, map[string]interface{}{"type": "text", "text": text})
			} else {
				slog.Warn("Binary embedded resource cannot be converted to text, skipping")
			}
		default:
			slog.Warn("Unsupported content type in tool result, skipping", "type", itemType)
		}
	}
	return converted, ""
}

// mcpResultResponse builds a JSON-RPC result response.
func mcpResultResponse(id interface{}, result map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
}

// mcpErrorResponse builds a JSON-RPC error response.
func mcpErrorResponse(id interface{}, code int, message string) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
}

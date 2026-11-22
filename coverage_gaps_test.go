package claude

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal"
	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// TestCoveragePermissionSuggestionsFromInternal covers every branch of the
// public-side permission suggestion converter.
func TestCoveragePermissionSuggestionsFromInternal(t *testing.T) {
	if got := permissionSuggestionsFromInternal(nil); got != nil {
		t.Errorf("nil input must stay nil, got %v", got)
	}
	in := []internal.PermissionUpdate{
		{
			Type: "addRules", Behavior: "allow", Mode: "acceptEdits",
			Destination: "localSettings",
			Directories: []string{"/tmp/a", "/tmp/b"},
			Rules: []internal.PermissionRuleValue{
				{ToolName: "Bash", RuleContent: "ls", RuleContentIsSet: true},
				{ToolName: "Read", RuleContentIsSet: true},
			},
		},
		{Type: "setMode", Mode: "plan"},
	}
	got := permissionSuggestionsFromInternal(in)
	if len(got) != 2 {
		t.Fatalf("Expected 2 updates, got %d", len(got))
	}
	first := got[0]
	if first.Type != "addRules" || first.Mode != PermissionMode("acceptEdits") ||
		first.Destination != "localSettings" || first.Behavior != "allow" {
		t.Errorf("Scalar fields wrong: %+v", first)
	}
	if !reflect.DeepEqual(first.Directories, []string{"/tmp/a", "/tmp/b"}) {
		t.Errorf("Directories wrong: %v", first.Directories)
	}
	if len(first.Rules) != 2 || first.Rules[0].RuleContent != "ls" ||
		!first.Rules[0].RuleContentIsSet || first.Rules[1].ToolName != "Read" ||
		!first.Rules[1].RuleContentIsSet {
		t.Errorf("Rules wrong: %+v", first.Rules)
	}
	if got[1].Directories != nil || got[1].Rules != nil {
		t.Errorf("Nil slices must stay nil: %+v", got[1])
	}
}

// TestCoveragePermissionUpdatesToInternal covers the nil input and the
// empty-but-non-nil slice branches.
func TestCoveragePermissionUpdatesToInternal(t *testing.T) {
	if got := permissionUpdatesToInternal(nil); got != nil {
		t.Errorf("nil input must stay nil, got %v", got)
	}
	in := []PermissionUpdate{
		{
			Type: "addDirectories", Mode: PermissionMode("plan"),
			Directories: []string{}, Rules: []PermissionRuleValue{},
		},
		{
			Type: "addRules",
			Rules: []PermissionRuleValue{
				{ToolName: "Bash", RuleContentIsSet: true},
			},
		},
	}
	got := permissionUpdatesToInternal(in)
	if len(got) != 2 {
		t.Fatalf("Expected 2 updates, got %d", len(got))
	}
	if got[0].Directories == nil || len(got[0].Directories) != 0 {
		t.Errorf("Empty Directories must stay non-nil: %v", got[0].Directories)
	}
	if got[0].Rules == nil || len(got[0].Rules) != 0 {
		t.Errorf("Empty Rules must stay non-nil: %v", got[0].Rules)
	}
	if got[0].Mode != "plan" {
		t.Errorf("Mode conversion wrong: %q", got[0].Mode)
	}
	if len(got[1].Rules) != 1 || !got[1].Rules[0].RuleContentIsSet {
		t.Errorf("RuleContentIsSet lost: %+v", got[1].Rules)
	}
	if got[1].Directories != nil || got[1].Rules == nil {
		t.Errorf("Nil Directories must stay nil: %+v", got[1])
	}
}

// TestCoverageParseTypedHookInputErrors covers both decode error paths.
func TestCoverageParseTypedHookInputErrors(t *testing.T) {
	t.Run("marshal error", func(t *testing.T) {
		_, err := ParseTypedHookInput(map[string]interface{}{
			"hook_event_name": "PreToolUse",
			"tool_input":      map[string]interface{}{"fn": func() {}},
		})
		if err == nil {
			t.Fatal("Expected a marshal error")
		}
	})
	t.Run("unmarshal error", func(t *testing.T) {
		_, err := ParseTypedHookInput(map[string]interface{}{
			"hook_event_name": "PostToolUse",
			"duration_ms":     "not-a-number",
		})
		if err == nil {
			t.Fatal("Expected an unmarshal error")
		}
	})
}

// TestCoverageStringSliceFromInterface covers all type switches.
func TestCoverageStringSliceFromInterface(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  []string
	}{
		{"nil", nil, nil},
		{"nil string slice", []string(nil), nil},
		{"empty string slice", []string{}, []string{}},
		{"string slice", []string{"a", "b"}, []string{"a", "b"}},
		{"interface slice", []interface{}{"a", "b"}, []string{"a", "b"}},
		{"interface slice with non-string", []interface{}{"a", 1}, nil},
		{"integer", 42, nil},
		{"map", map[string]string{"a": "b"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringSliceFromInterface(tt.value)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Got %#v, want %#v", got, tt.want)
			}
			if tt.want != nil && got == nil {
				t.Error("An empty report must stay non-nil")
			}
		})
	}
}

// TestCoverageHookSpecificOutputMapPanics covers both panic guards.
func TestCoverageHookSpecificOutputMapPanics(t *testing.T) {
	t.Run("marshal failure", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil || !strings.Contains(r.(string), "marshaling") {
				t.Errorf("Expected a marshaling panic, got %v", r)
			}
		}()
		HookSpecificOutputMap(func() {})
	})
	t.Run("unmarshal failure", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil || !strings.Contains(r.(string), "unmarshaling") {
				t.Errorf("Expected an unmarshaling panic, got %v", r)
			}
		}()
		HookSpecificOutputMap(42)
	})
}

// TestCoverageMarshalHookSpecificOutputErrors covers both error returns.
func TestCoverageMarshalHookSpecificOutputErrors(t *testing.T) {
	if _, err := marshalHookSpecificOutput("PreToolUse", func() {}); err == nil {
		t.Error("Expected a marshal error")
	}
	if _, err := marshalHookSpecificOutput("PreToolUse", 42); err == nil {
		t.Error("Expected an unmarshal error")
	}
}

// TestCoverageReceiveMessagesSkipAndCancel covers unparseable frames and
// context cancellation.
func TestCoverageReceiveMessagesSkipAndCancel(t *testing.T) {
	mockT := newMockTransport()
	handleInitialization(mockT, nil)
	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := client.ReceiveMessages(ctx)
	if err != nil {
		t.Fatalf("ReceiveMessages failed: %v", err)
	}

	mockT.readCh <- map[string]interface{}{"type": "assistant"} // unparseable
	mockT.readCh <- gapAssistantFrame("ok")

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("stream closed before the valid message")
		}
		if _, valid := msg.(*AssistantMessage); !valid {
			t.Errorf("Expected *AssistantMessage, got %T", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the valid message")
	}

	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("Expected the channel to close after cancel")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not close the ReceiveMessages channel")
	}
}

// covConnectedClient builds a client wired to a MockTransport.
func covConnectedClient(t *testing.T) (*ClaudeSDKClient, *MockTransport) {
	t.Helper()
	mockT := newMockTransport()
	handleInitialization(mockT, nil)
	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	return client, mockT
}

// drainClosedChannel waits for a channel to close, discarding buffered items.
func drainClosedChannel(t *testing.T, ch <-chan Message) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel did not close")
		}
	}
}

// TestCoverageReceiveMessagesTransportEOF covers rawMessages closing.
func TestCoverageReceiveMessagesTransportEOF(t *testing.T) {
	client, _ := covConnectedClient(t)
	ch, err := client.ReceiveMessages(context.Background())
	if err != nil {
		t.Fatalf("ReceiveMessages failed: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	drainClosedChannel(t, ch)
}

// TestCoverageReceiveMessagesBackpressureCancel fills the output buffer so
// the inner select takes the ctx.Done branch.
func TestCoverageReceiveMessagesBackpressureCancel(t *testing.T) {
	client, mockT := covConnectedClient(t)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := client.ReceiveMessages(ctx)
	if err != nil {
		t.Fatalf("ReceiveMessages failed: %v", err)
	}

	go func() {
		for i := 0; i < 150; i++ {
			select {
			case mockT.readCh <- gapAssistantFrame("fill"):
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	drainClosedChannel(t, ch)
}

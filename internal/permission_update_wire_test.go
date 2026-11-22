package internal

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// jsonMarshal renders a wire value for comparison; encoding/json sorts map
// keys, so the output is deterministic.
func jsonMarshal(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "<marshal error>"
	}
	return string(data)
}

// TestPermissionUpdateWholeToolRuleOmitsRuleContent mirrors Python's
// test_whole_tool_rule_has_no_rule_content_key: Claude Code rejects a null
// ruleContent, so an unset value leaves the key out.
func TestPermissionUpdateWholeToolRuleOmitsRuleContent(t *testing.T) {
	got, err := permissionUpdateToWire(PermissionUpdate{
		Type:        "addRules",
		Rules:       []PermissionRuleValue{{ToolName: "WebSearch"}},
		Behavior:    "allow",
		Destination: "localSettings",
	})
	if err != nil {
		t.Fatalf("permissionUpdateToWire failed: %v", err)
	}
	want := map[string]interface{}{
		"type":        "addRules",
		"destination": "localSettings",
		"behavior":    "allow",
		"rules":       []map[string]interface{}{{"toolName": "WebSearch"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Expected %v, got %v", want, got)
	}
}

// TestPermissionUpdateEmptyRuleContentSentAsIs mirrors Python's
// test_empty_rule_content_is_sent_as_is. RuleContentIsSet is the Go marker
// for an explicit empty ruleContent.
func TestPermissionUpdateEmptyRuleContentSentAsIs(t *testing.T) {
	got, err := permissionUpdateToWire(PermissionUpdate{
		Type:        "addRules",
		Rules:       []PermissionRuleValue{{ToolName: "Bash", RuleContentIsSet: true}},
		Behavior:    "deny",
		Destination: "session",
	})
	if err != nil {
		t.Fatalf("permissionUpdateToWire failed: %v", err)
	}
	rules, ok := got["rules"].([]map[string]interface{})
	if !ok || len(rules) != 1 {
		t.Fatalf("Expected one wire rule, got %v", got["rules"])
	}
	if value, present := rules[0]["ruleContent"]; !present || value != "" {
		t.Errorf("Expected ruleContent=\"\" to be sent, got %v (present=%v)", value, present)
	}
}

// TestPermissionUpdateMissingDestinationIsSession mirrors Python's
// test_missing_destination_is_sent_as_session.
func TestPermissionUpdateMissingDestinationIsSession(t *testing.T) {
	updates := []PermissionUpdate{
		{Type: "addRules", Rules: []PermissionRuleValue{{ToolName: "Read"}}, Behavior: "allow"},
		{Type: "replaceRules", Rules: []PermissionRuleValue{{ToolName: "Read"}}, Behavior: "deny"},
		{Type: "removeRules", Rules: []PermissionRuleValue{{ToolName: "Read"}}, Behavior: "allow"},
		{Type: "setMode", Mode: "acceptEdits"},
		{Type: "addDirectories", Directories: []string{"/tmp/a"}},
		{Type: "removeDirectories", Directories: []string{"/tmp/a"}},
	}
	for _, update := range updates {
		t.Run(update.Type, func(t *testing.T) {
			got, err := permissionUpdateToWire(update)
			if err != nil {
				t.Fatalf("permissionUpdateToWire failed: %v", err)
			}
			if got["destination"] != "session" {
				t.Errorf("Expected destination=session, got %v", got["destination"])
			}
		})
	}
}

// TestPermissionUpdateMissingRequiredFieldRaises mirrors Python's
// test_missing_required_field_raises.
func TestPermissionUpdateMissingRequiredFieldRaises(t *testing.T) {
	tests := []struct {
		update  PermissionUpdate
		missing string
	}{
		{PermissionUpdate{Type: "addRules", Behavior: "allow"}, "rules"},
		{PermissionUpdate{Type: "replaceRules", Behavior: "deny"}, "rules"},
		{PermissionUpdate{Type: "removeRules", Behavior: "ask"}, "rules"},
		{PermissionUpdate{Type: "addRules", Rules: []PermissionRuleValue{{ToolName: "Read"}}}, "behavior"},
		{PermissionUpdate{Type: "replaceRules", Rules: []PermissionRuleValue{{ToolName: "Read"}}}, "behavior"},
		{PermissionUpdate{Type: "removeRules", Rules: []PermissionRuleValue{{ToolName: "Read"}}}, "behavior"},
		{PermissionUpdate{Type: "setMode"}, "mode"},
		{PermissionUpdate{Type: "addDirectories"}, "directories"},
		{PermissionUpdate{Type: "removeDirectories"}, "directories"},
	}
	for _, tt := range tests {
		t.Run(tt.update.Type+"/"+tt.missing, func(t *testing.T) {
			_, err := permissionUpdateToWire(tt.update)
			if err == nil {
				t.Fatal("Expected an error for a missing required field")
			}
			if !strings.Contains(err.Error(), "has no "+tt.missing) {
				t.Errorf("Expected the error to name %q, got %q", tt.missing, err.Error())
			}
		})
	}
}

// TestPermissionUpdateUnknownTypeRaises mirrors Python's
// test_unknown_type_raises.
func TestPermissionUpdateUnknownTypeRaises(t *testing.T) {
	_, err := permissionUpdateToWire(PermissionUpdate{Type: "addSomething", Destination: "session"})
	if err == nil {
		t.Fatal("Expected an error for an unknown update type")
	}
	if !strings.Contains(err.Error(), "which this version") {
		t.Errorf("Expected the error to explain the unsupported type, got %q", err.Error())
	}
}

// TestPermissionSuggestionRoundTripsUnchanged mirrors Python's
// test_cli_suggestion_round_trips_unchanged: echoing a CLI suggestion sends
// back exactly what the CLI sent.
func TestPermissionSuggestionRoundTripsUnchanged(t *testing.T) {
	wires := []map[string]interface{}{
		{
			"type":        "addRules",
			"destination": "localSettings",
			"behavior":    "allow",
			"rules":       []interface{}{map[string]interface{}{"toolName": "WebSearch"}},
		},
		{
			"type":        "replaceRules",
			"destination": "projectSettings",
			"behavior":    "deny",
			"rules":       []interface{}{map[string]interface{}{"toolName": "Bash", "ruleContent": "rm -rf *"}},
		},
		{
			"type":        "removeRules",
			"destination": "userSettings",
			"behavior":    "ask",
			"rules":       []interface{}{map[string]interface{}{"toolName": "mcp__server__tool"}},
		},
		{"type": "setMode", "mode": "plan", "destination": "session"},
		{
			"type":        "removeDirectories",
			"directories": []interface{}{"/tmp/a"},
			"destination": "localSettings",
		},
	}
	for _, wire := range wires {
		t.Run(wire["type"].(string), func(t *testing.T) {
			got, err := permissionUpdateToWire(permissionUpdateFromMap(wire))
			if err != nil {
				t.Fatalf("permissionUpdateToWire failed: %v", err)
			}
			// Normalise through JSON so slice types match.
			gotJSON := jsonMarshal(got)
			wantJSON := jsonMarshal(wire)
			if gotJSON != wantJSON {
				t.Errorf("Expected %s, got %s", wantJSON, gotJSON)
			}
		})
	}
}

// TestCanUseToolPermissionUpdatesAreAllOrNothing mirrors Python's
// test_permission_callback_sends_no_updates_if_one_is_malformed. A failed
// conversion drops every update but still allows the tool call.
func TestCanUseToolPermissionUpdatesAreAllOrNothing(t *testing.T) {
	canUseTool := func(string, map[string]interface{}, ToolPermissionContext) (PermissionResult, error) {
		return &PermissionResultAllow{
			UpdatedPermissions: []PermissionUpdate{
				{Type: "addRules", Rules: []PermissionRuleValue{{ToolName: "Read"}}, Behavior: "allow"},
				{Type: "setMode"}, // malformed: no mode
			},
		}, nil
	}
	q := NewQuery(QueryConfig{
		Transport:       newMockTransport(),
		IsStreamingMode: true,
		CanUseTool:      canUseTool,
	})
	response, err := q.handleCanUseTool(context.Background(), map[string]interface{}{
		"tool_name": "Bash",
		"input":     map[string]interface{}{"command": "ls"},
	})
	if err != nil {
		t.Fatalf("handleCanUseTool failed: %v", err)
	}
	if response["behavior"] != "allow" {
		t.Errorf("Expected the tool call to stay allowed, got %v", response["behavior"])
	}
	if _, present := response["updatedPermissions"]; present {
		t.Errorf("Expected no permission updates to be sent, got %v", response["updatedPermissions"])
	}
}

// TestCanUseToolEchoesWholeToolSuggestion mirrors Python's
// test_permission_callback_echoes_whole_tool_suggestion.
func TestCanUseToolEchoesWholeToolSuggestion(t *testing.T) {
	canUseTool := func(_ string, _ map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		return &PermissionResultAllow{UpdatedPermissions: ctx.Suggestions}, nil
	}
	q := NewQuery(QueryConfig{
		Transport:       newMockTransport(),
		IsStreamingMode: true,
		CanUseTool:      canUseTool,
	})
	response, err := q.handleCanUseTool(context.Background(), map[string]interface{}{
		"tool_name": "WebSearch",
		"input":     map[string]interface{}{},
		"permission_suggestions": []interface{}{
			map[string]interface{}{
				"type":        "addRules",
				"destination": "localSettings",
				"behavior":    "allow",
				"rules":       []interface{}{map[string]interface{}{"toolName": "WebSearch"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("handleCanUseTool failed: %v", err)
	}
	wire, ok := response["updatedPermissions"].([]map[string]interface{})
	if !ok || len(wire) != 1 {
		t.Fatalf("Expected one wire update, got %T %v", response["updatedPermissions"], response["updatedPermissions"])
	}
	rules, ok := wire[0]["rules"].([]map[string]interface{})
	if !ok || len(rules) != 1 {
		t.Fatalf("Expected one wire rule, got %v", wire[0]["rules"])
	}
	if _, present := rules[0]["ruleContent"]; present {
		t.Errorf("Whole-tool suggestion must keep ruleContent absent, got %v", rules[0])
	}
}

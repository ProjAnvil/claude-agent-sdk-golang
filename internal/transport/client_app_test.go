package transport

import (
	"context"
	"testing"
)

// clientAppEnv reads the child env of a transport built with opts.
func clientAppEnv(t *testing.T, opts *TransportOptions) map[string][]string {
	t.Helper()
	tr := newTestTransport(t, opts)
	return envMap(t, tr.buildCommand(context.Background()).Env)
}

// clientAppPtr returns a pointer to s.
func clientAppPtr(s string) *string { return &s }

// TestClientAppSetsExplicitEnv mirrors Python's
// test_client_app_is_explicit_and_stops_derivation. The Go SDK never derives
// a label, so only CLAUDE_AGENT_SDK_CLIENT_APP is set.
func TestClientAppSetsExplicitEnv(t *testing.T) {
	unsetEnvForTest(t, explicitClientAppEnv)
	unsetEnvForTest(t, derivedClientAppEnv)
	m := clientAppEnv(t, &TransportOptions{ClientApp: clientAppPtr("my-app/1.0.0")})
	if got := m[explicitClientAppEnv]; len(got) != 1 || got[0] != "my-app/1.0.0" {
		t.Errorf("Expected one explicit client app label, got %v", got)
	}
	if got := m[derivedClientAppEnv]; len(got) != 0 {
		t.Errorf("The Go SDK must never derive a client app label, got %v", got)
	}
}

// TestClientAppWinsOverOptionsEnv mirrors Python's
// test_client_app_wins_over_options_env.
func TestClientAppWinsOverOptionsEnv(t *testing.T) {
	unsetEnvForTest(t, explicitClientAppEnv)
	m := clientAppEnv(t, &TransportOptions{
		ClientApp: clientAppPtr("from-option"),
		Env:       map[string]string{explicitClientAppEnv: "from-env"},
	})
	got := m[explicitClientAppEnv]
	if len(got) != 2 {
		t.Fatalf("Expected both the env value and the option value, got %v", got)
	}
	// os/exec keeps the last value for a repeated key.
	if got[len(got)-1] != "from-option" {
		t.Errorf("Expected the option to win, last value is %q in %v", got[len(got)-1], got)
	}
}

// TestOptionsEnvIsNotReplaced mirrors Python's
// test_options_env_stops_derivation and test_empty_env_value_is_the_opt_out.
func TestOptionsEnvIsNotReplaced(t *testing.T) {
	unsetEnvForTest(t, explicitClientAppEnv)
	unsetEnvForTest(t, derivedClientAppEnv)
	for _, value := range []string{"from-env", ""} {
		t.Run("value="+value, func(t *testing.T) {
			m := clientAppEnv(t, &TransportOptions{
				Env: map[string]string{explicitClientAppEnv: value},
			})
			if got := m[explicitClientAppEnv]; len(got) != 1 || got[len(got)-1] != value {
				t.Errorf("Expected the caller's explicit value to survive, got %v", got)
			}
			if got := m[derivedClientAppEnv]; len(got) != 0 {
				t.Errorf("Expected no derived label, got %v", got)
			}
		})
	}
}

// TestEmptyClientAppIsTheOptOut mirrors Python's
// test_empty_client_app_is_the_opt_out: a nil pointer means "not set", a
// pointer to "" explicitly sends no identifier.
func TestEmptyClientAppIsTheOptOut(t *testing.T) {
	unsetEnvForTest(t, explicitClientAppEnv)
	m := clientAppEnv(t, &TransportOptions{ClientApp: clientAppPtr("")})
	got := m[explicitClientAppEnv]
	if len(got) != 1 || got[0] != "" {
		t.Errorf("Expected an explicit empty label, got %v", got)
	}
}

// TestInheritedDerivedClientAppIsRemoved mirrors Python's
// test_replaces_a_derived_label_inherited_from_an_outer_session and
// test_removes_an_inherited_derived_label_on_opt_out.
func TestInheritedDerivedClientAppIsRemoved(t *testing.T) {
	t.Setenv(derivedClientAppEnv, "outer-app/9.9.9")
	unsetEnvForTest(t, explicitClientAppEnv)
	for _, opts := range []*TransportOptions{
		{},
		{ClientApp: clientAppPtr("")},
		{Env: map[string]string{derivedClientAppEnv: "from-options-env"}},
	} {
		m := clientAppEnv(t, opts)
		if got := m[derivedClientAppEnv]; len(got) != 0 {
			t.Errorf("Expected the inherited derived label to be stripped, got %v", got)
		}
	}
}

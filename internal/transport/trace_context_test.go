package transport

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// spanContext creates a sampled W3C span context for trace propagation tests.
func spanContextForTraceTest(t *testing.T) context.Context {
	t.Helper()
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	})
	if !sc.IsValid() {
		t.Fatal("test span context is invalid")
	}
	return trace.ContextWithSpanContext(context.Background(), sc)
}

// TestInjectTraceContextFromActiveSpan mirrors Python's OTEL propagation
// block: an active span reaches the CLI as TRACEPARENT.
func TestInjectTraceContextFromActiveSpan(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{})
	m := envMap(t, tr.buildCommand(spanContextForTraceTest(t)).Env)
	values := m[traceParentEnv]
	if len(values) != 1 {
		t.Fatalf("Expected one TRACEPARENT, got %v", values)
	}
	if !strings.HasPrefix(values[0], "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01") {
		t.Errorf("Unexpected TRACEPARENT: %q", values[0])
	}
}

// TestInjectTraceContextScrubsStaleInherited prevents a stale inherited
// TRACEPARENT from being paired with a fresh TRACESTATE.
func TestInjectTraceContextScrubsStaleInherited(t *testing.T) {
	t.Setenv(traceParentEnv, "00-ffffffffffffffffffffffffffffffff-ffffffffffffffff-00")
	t.Setenv(traceStateEnv, "vendor=stale")
	tr := newTestTransport(t, &TransportOptions{})
	m := envMap(t, tr.buildCommand(spanContextForTraceTest(t)).Env)
	if got := m[traceParentEnv]; len(got) != 1 || strings.Contains(got[0], "ffffffff") {
		t.Errorf("Expected the stale TRACEPARENT to be replaced, got %v", got)
	}
	if got := m[traceStateEnv]; len(got) != 0 {
		t.Errorf("Expected the stale TRACESTATE to be removed, got %v", got)
	}
}

// TestInjectTraceContextExplicitEnvWins keeps the caller's explicit env
// values, matching Python's options.env precedence.
func TestInjectTraceContextExplicitEnvWins(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{
		Env: map[string]string{traceParentEnv: "caller-value"},
	})
	m := envMap(t, tr.buildCommand(spanContextForTraceTest(t)).Env)
	got := m[traceParentEnv]
	if len(got) != 1 || got[0] != "caller-value" {
		t.Errorf("Expected the caller's TRACEPARENT to win, got %v", got)
	}
}

// TestInjectTraceContextNoSpanIsNoOp leaves inherited values untouched when
// no span is active, as the Python SDK does.
func TestInjectTraceContextNoSpanIsNoOp(t *testing.T) {
	t.Setenv(traceParentEnv, "inherited-value")
	unsetEnvForTest(t, traceStateEnv)
	tr := newTestTransport(t, &TransportOptions{})
	m := envMap(t, tr.buildCommand(context.Background()).Env)
	if got := m[traceParentEnv]; len(got) != 1 || got[0] != "inherited-value" {
		t.Errorf("Expected the inherited TRACEPARENT to survive, got %v", got)
	}
	if got := m[traceStateEnv]; len(got) != 0 {
		t.Errorf("Expected no TRACESTATE without an active span, got %v", got)
	}
}

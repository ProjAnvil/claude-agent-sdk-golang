package transport

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// --- #1198: --resume-session-at / --resume-drops-turn ------------------------

// strPtr returns a pointer to s (test helper for ResumeDropsTurn).
func strPtr(s string) *string { return &s }

// TestBuildCommand_ResumeSessionAtAndDropsTurn mirrors the Python
// test_build_command_resume_session_at_and_drops_turn: the truncating-resume
// options are passed as --flag=value.
func TestBuildCommand_ResumeSessionAtAndDropsTurn(t *testing.T) {
	at := "0d78eb23-2d48-4741-b970-4ed0a3356cce"
	drops := "ce0a8011-2c8d-40f2-86e5-d6e1b0c041c0"
	tr := newTestTransport(t, &TransportOptions{
		Resume:          "abc123",
		ForkSession:     true,
		ResumeSessionAt: at,
		ResumeDropsTurn: strPtr(drops),
	})

	cmd := tr.buildCommand(context.Background())
	args := cmd.Args[1:]

	if !containsToken(args, "--resume-session-at="+at) {
		t.Errorf("Expected --resume-session-at=%s in args: %v", at, args)
	}
	if !containsToken(args, "--resume-drops-turn="+drops) {
		t.Errorf("Expected --resume-drops-turn=%s in args: %v", drops, args)
	}
	// Equals form only: the flag and the value never appear as standalone
	// argv elements.
	if containsToken(args, "--resume-session-at") {
		t.Errorf("--resume-session-at must not appear as a bare flag: %v", args)
	}
	if containsToken(args, "--resume-drops-turn") {
		t.Errorf("--resume-drops-turn must not appear as a bare flag: %v", args)
	}
	if containsToken(args, at) {
		t.Errorf("ResumeSessionAt value must not appear as a standalone arg: %v", args)
	}
	if containsToken(args, drops) {
		t.Errorf("ResumeDropsTurn value must not appear as a standalone arg: %v", args)
	}
}

// TestBuildCommand_ResumeDropsTurnOmittedByDefault mirrors the Python
// test_build_command_resume_drops_turn_omitted_by_default.
func TestBuildCommand_ResumeDropsTurnOmittedByDefault(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{
		Resume:          "abc123",
		ResumeSessionAt: "x",
	})

	cmd := tr.buildCommand(context.Background())
	args := cmd.Args[1:]

	if !containsToken(args, "--resume-session-at=x") {
		t.Errorf("Expected --resume-session-at=x in args: %v", args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--resume-drops-turn") {
			t.Errorf("--resume-drops-turn must be omitted when nil, got %q in %v", a, args)
		}
	}
}

// TestBuildCommand_EmptyResumeDropsTurnIsForwarded mirrors the Python
// test_build_command_empty_resume_drops_turn_is_forwarded: an empty
// declaration must reach the CLI (which rejects it) rather than being dropped
// here and silently disarming the guard.
func TestBuildCommand_EmptyResumeDropsTurnIsForwarded(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{
		Resume:          "abc123",
		ResumeSessionAt: "x",
		ResumeDropsTurn: strPtr(""),
	})

	cmd := tr.buildCommand(context.Background())
	args := cmd.Args[1:]

	if !containsToken(args, "--resume-drops-turn=") {
		t.Errorf("Expected --resume-drops-turn= in args: %v", args)
	}
}

// TestRejectWindowsCmdMetacharacters_TruncatingResume mirrors the Python
// parametrized test_bad_truncating_resume_values_raise_on_windows: both new
// options get the same cmd.exe metacharacter rejection as resume/session_id.
func TestRejectWindowsCmdMetacharacters_TruncatingResume(t *testing.T) {
	for _, option := range []string{"resume_session_at", "resume_drops_turn"} {
		err := rejectWindowsCmdMetacharactersForGOOS(option, "x&calc", "windows")
		if err == nil {
			t.Errorf("Expected rejection for %s on Windows", option)
			continue
		}
		if !strings.Contains(err.Error(), option) {
			t.Errorf("Error should name the option %s: %v", option, err)
		}
		// POSIX is a no-op.
		if err := rejectWindowsCmdMetacharactersForGOOS(option, "x&calc", "linux"); err != nil {
			t.Errorf("POSIX should allow metacharacters for %s: %v", option, err)
		}
	}
}

// TestConnect_TruncatingResumeMetacharacterRejection verifies Connect surfaces
// the rejection for both new options before any spawn on Windows.
func TestConnect_TruncatingResumeMetacharacterRejection(t *testing.T) {
	for _, opts := range []*TransportOptions{
		{Resume: "abc", ResumeSessionAt: "x&calc"},
		{Resume: "abc", ResumeDropsTurn: strPtr("x&calc")},
	} {
		tr := newTestTransport(t, opts)
		tr.goos = "windows"

		err := tr.Connect(context.Background())
		if err == nil {
			t.Fatal("Expected Connect to reject cmd.exe metacharacters on Windows")
		}
		var connErr *CLIConnectionError
		if errors.As(err, &connErr) {
			t.Errorf("Metacharacter rejection should be a plain error, got %T", err)
		}
		if tr.process != nil {
			t.Error("Rejection must happen before any spawn; transport.process should be nil")
		}
	}
}

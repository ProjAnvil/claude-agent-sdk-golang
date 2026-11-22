package transport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// settingsValue extracts the --settings argv value from a built command.
func settingsValue(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	args := cmd.Args[1:]
	for i, arg := range args {
		if arg == "--settings" && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatal("Expected a --settings value")
	return ""
}

// TestBuildSettingsRelativeToCWD mirrors Python's
// test_build_command_sandbox_reads_relative_settings_from_cwd.
func TestBuildSettingsRelativeToCWD(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"permissions":{"deny":["Bash"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := newTestTransport(t, &TransportOptions{
		Settings: "settings.json",
		CWD:      dir,
		Sandbox:  &SandboxSettings{Enabled: true},
	})
	value := settingsValue(t, tr.buildCommand(context.Background()))
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		t.Fatalf("Failed to parse merged settings: %v", err)
	}
	permissions, ok := parsed["permissions"].(map[string]interface{})
	if !ok || len(permissions) == 0 {
		t.Errorf("Expected the file's permission rules in merged settings, got %v", parsed)
	}
	if _, ok := parsed["sandbox"]; !ok {
		t.Errorf("Expected a sandbox key in merged settings, got %v", parsed)
	}
}

// TestBuildSettingsAbsolutePath mirrors Python's
// test_build_command_sandbox_reads_absolute_settings_path.
func TestBuildSettingsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"permissions":{"deny":["Bash"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := newTestTransport(t, &TransportOptions{
		Settings: path,
		CWD:      filepath.Join(dir, "elsewhere"),
		Sandbox:  &SandboxSettings{Enabled: true},
	})
	value := settingsValue(t, tr.buildCommand(context.Background()))
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		t.Fatalf("Failed to parse merged settings: %v", err)
	}
	if _, ok := parsed["permissions"]; !ok {
		t.Errorf("Expected the absolute settings file to be loaded, got %v", parsed)
	}
}

// TestBuildSettingsRelativeWithoutCWD mirrors Python's
// test_build_command_sandbox_relative_settings_without_cwd.
func TestBuildSettingsRelativeWithoutCWD(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"m"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	tr := newTestTransport(t, &TransportOptions{
		Settings: "settings.json",
		Sandbox:  &SandboxSettings{Enabled: true},
	})
	value := settingsValue(t, tr.buildCommand(context.Background()))
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		t.Fatalf("Failed to parse merged settings: %v", err)
	}
	if parsed["model"] != "m" {
		t.Errorf("Expected model=m from the process cwd, got %v", parsed)
	}
}

// TestBuildSettingsUnloadableRaises mirrors Python's
// test_build_command_sandbox_unloadable_settings_raise. The error must not
// contain inline settings text, which can hold secrets.
func TestBuildSettingsUnloadableRaises(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		contents string
	}{
		{"missing file", "missing.json", ""},
		{"directory", ".", ""},
		{"malformed file", "settings.json", "{not json"},
		{"JSON array", "settings.json", "[1, 2]"},
		{"malformed inline JSON", `{"env": {"KEY": "secret-value",}}`, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.contents != "" {
				if err := os.WriteFile(filepath.Join(dir, tt.settings), []byte(tt.contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			tr := newTestTransport(t, &TransportOptions{
				Settings: tt.settings,
				CWD:      dir,
				Sandbox:  &SandboxSettings{Enabled: true},
			})
			_, err := tr.buildCommandChecked(context.Background())
			if err == nil {
				t.Fatal("Expected settings that cannot be loaded to raise")
			}
			var connErr *CLIConnectionError
			if !errors.As(err, &connErr) {
				t.Fatalf("Expected *CLIConnectionError, got %T: %v", err, err)
			}
			if !strings.Contains(connErr.Message, "settings") {
				t.Errorf("Expected the error to mention settings, got %q", connErr.Message)
			}
			if strings.Contains(connErr.Message, "secret-value") {
				t.Errorf("The error must not echo inline settings content: %q", connErr.Message)
			}
		})
	}
}

// TestBuildSettingsWithBOM mirrors Python's
// test_build_command_sandbox_reads_settings_file_with_bom.
func TestBuildSettingsWithBOM(t *testing.T) {
	dir := t.TempDir()
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"permissions":{"deny":["Bash"]}}`)...)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	tr := newTestTransport(t, &TransportOptions{
		Settings: "settings.json",
		CWD:      dir,
		Sandbox:  &SandboxSettings{Enabled: true},
	})
	value := settingsValue(t, tr.buildCommand(context.Background()))
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		t.Fatalf("BOM settings file must load, got %v: %s", err, value)
	}
	if _, ok := parsed["permissions"]; !ok {
		t.Errorf("Expected the BOM file's permissions, got %v", parsed)
	}
}

// TestBuildSettingsInlineJSONAndNull covers the inline object and JSON null
// branches of buildSettingsValue. A file containing "null" decodes to a nil
// map, which the merge must replace with an empty object.
func TestBuildSettingsInlineJSONAndNull(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "null-settings.json"), []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name     string
		settings string
		wantKey  string
	}{
		{"inline object", `{"model":"m"}`, "model"},
		{"null settings file", "null-settings.json", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTestTransport(t, &TransportOptions{
				Settings: tt.settings,
				CWD:      dir,
				Sandbox:  &SandboxSettings{Enabled: true},
			})
			value := settingsValue(t, tr.buildCommand(context.Background()))
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(value), &parsed); err != nil {
				t.Fatalf("Failed to parse merged settings: %v", err)
			}
			if _, ok := parsed["sandbox"]; !ok {
				t.Errorf("Expected a sandbox key, got %v", parsed)
			}
			if tt.wantKey != "" {
				if parsed[tt.wantKey] != "m" {
					t.Errorf("Expected %s=m, got %v", tt.wantKey, parsed)
				}
			} else if len(parsed) != 1 {
				t.Errorf("Expected only the sandbox key, got %v", parsed)
			}
		})
	}
}

// TestBuildSettingsWithoutSandboxPassesThrough keeps the caller's settings
// untouched when no sandbox merge is needed.
func TestBuildSettingsWithoutSandboxPassesThrough(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{Settings: "settings.json"})
	value := settingsValue(t, tr.buildCommand(context.Background()))
	if value != "settings.json" {
		t.Errorf("Expected the settings path to pass through, got %q", value)
	}
}

// TestConnectRejectsUnloadableSettings proves the error reaches Connect, not
// just buildCommandChecked.
func TestConnectRejectsUnloadableSettings(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{
		Settings: "missing.json",
		CWD:      t.TempDir(),
		Sandbox:  &SandboxSettings{Enabled: true},
	})
	err := tr.Connect(context.Background())
	if err == nil {
		t.Fatal("Expected Connect to reject unloadable settings")
	}
	var connErr *CLIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Expected *CLIConnectionError, got %T: %v", err, err)
	}
}

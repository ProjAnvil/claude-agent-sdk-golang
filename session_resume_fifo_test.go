//go:build !windows

package claude

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestCopyAuthFiles_FIFOSeedFileSkipped mirrors the Python SDK's
// test_fifo_seed_file_is_skipped_not_read: a FIFO where settings.json is
// expected would block a plain read forever; it must be skipped like any
// other non-regular file.
func TestCopyAuthFiles_FIFOSeedFileSkipped(t *testing.T) {
	configDir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(configDir, "settings.json"), 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	tmpBase := t.TempDir()
	done := make(chan error, 1)
	go func() { done <- copyAuthFiles(tmpBase, map[string]string{"CLAUDE_CONFIG_DIR": configDir}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("copyAuthFiles: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copyAuthFiles blocked on FIFO settings.json")
	}
	if _, err := os.Stat(filepath.Join(tmpBase, "settings.json")); !os.IsNotExist(err) {
		t.Errorf("FIFO settings.json must be skipped, stat err=%v", err)
	}
}

// TestReadIfPresent_PermissionDeniedSkipped verifies that an unreadable
// regular file is logged and skipped rather than aborting the resume.
func TestReadIfPresent_PermissionDeniedSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based permission checks do not apply to root")
	}
	src := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(src, []byte(`{"a":1}`), 0o000); err != nil {
		t.Fatal(err)
	}
	if content := readIfPresent(src); content != nil {
		t.Errorf("unreadable file should be skipped, got %q", content)
	}

	// A stat failure other than "not exists" (here: the parent directory is
	// not searchable) is also logged and skipped.
	lockedDir := t.TempDir()
	if err := os.Chmod(lockedDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedDir, 0o700) })
	if content := readIfPresent(filepath.Join(lockedDir, "settings.json")); content != nil {
		t.Errorf("unstat-able file should be skipped, got %q", content)
	}
}

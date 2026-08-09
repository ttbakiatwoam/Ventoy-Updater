package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestListImagesSkipsMacProtectedDirs(t *testing.T) {
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "ubuntu-24.04.2-desktop-amd64.iso"), []byte("iso"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".TemporaryItems", ".Spotlight-V100", ".Trashes", ".fseventsd", ToolDirName} {
		if err := os.Mkdir(filepath.Join(target, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, name, "hidden.iso"), []byte("ignore"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := ListImages(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "ubuntu-24.04.2-desktop-amd64.iso" {
		t.Fatalf("files = %#v", files)
	}
}

func TestCleanupLeavesMacProtectedDirs(t *testing.T) {
	target := t.TempDir()
	protected := filepath.Join(target, ".TemporaryItems")
	if err := os.Mkdir(protected, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, ".DS_Store"), []byte("metadata"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Cleanup(target, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(protected); err != nil {
		t.Fatalf("protected directory was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".DS_Store")); !os.IsNotExist(err) {
		t.Fatalf(".DS_Store still exists or errored unexpectedly: %v", err)
	}
}

func TestProtectedDirNameIncludesTemporaryItems(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS metadata convention test")
	}
	if !isMacProtectedDirName(".TemporaryItems") {
		t.Fatal(".TemporaryItems should be treated as a protected metadata directory")
	}
}

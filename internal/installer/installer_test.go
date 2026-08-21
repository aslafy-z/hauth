package installer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMapping(t *testing.T) {
	for _, h := range []string{"gh", "claude", "codex", "opencode", "agy"} {
		if !Known(h) {
			t.Errorf("harness %s has no installer mapping", h)
		}
		if bin, ok := BinaryFor(h); !ok || bin == "" {
			t.Errorf("harness %s has no binary", h)
		}
	}
	if Known("fake") {
		t.Error("fake must not be installable")
	}
	if _, err := Argv("nope"); err == nil {
		t.Error("Argv must fail for unknown harness")
	}
}

func TestInstalledLooksAtPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH stub is unix-only")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if !Installed("claude") {
		t.Error("claude stub on PATH not detected")
	}
	if Installed("gh") {
		t.Error("gh reported installed with restricted PATH")
	}
	if Installed("unknown") {
		t.Error("unknown harness reported installed")
	}
}

func TestArgvUsesMise(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH stub is unix-only")
	}
	dir := t.TempDir()
	mise := filepath.Join(dir, "mise")
	if err := os.WriteFile(mise, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	argv, err := Argv("claude")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mise", "use", "-g", "claude-code@latest"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv = %v, want %v", argv, want)
		}
	}

	t.Setenv("PATH", t.TempDir())
	if _, err := Argv("claude"); err == nil {
		t.Error("Argv must fail without mise on PATH")
	}
}

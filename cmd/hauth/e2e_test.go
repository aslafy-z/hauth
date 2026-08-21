package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aslafy-z/hauth/internal/adapter"
)

// TestLoginPushRestoreE2E drives the full product path with the fake
// harness: `hauth login` runs the CLI in a pty, detects URL and code, takes
// pasted input, pushes the result to a file store, and a second home
// restores it via `hauth store restore`.
func TestLoginPushRestoreE2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty sessions are unix-only")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	fakecli := filepath.Join(repoRoot, "testdata", "fakecli", "fakecli")
	if _, err := os.Stat(fakecli); err != nil {
		t.Fatalf("fakecli script missing: %v", err)
	}

	storeRoot := t.TempDir()
	homeA := t.TempDir()
	homeB := t.TempDir()
	t.Setenv("HAUTH_ENABLE_FAKE", "1")
	t.Setenv("HAUTH_FAKE_CLI", fakecli)
	t.Setenv("HAUTH_USER", "e2e-user")
	// The registry is process-global and setup() registers adapters against
	// the current home, so both phases must agree on it.
	t.Setenv("HOME", homeA)
	t.Setenv("XDG_STATE_HOME", filepath.Join(homeA, ".local", "state"))

	run := func(stdin string, args ...string) (string, error) {
		cmd := newRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetIn(strings.NewReader(stdin))
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}

	base := []string{"--store", "file", "--store-path", storeRoot, "--instance", "ws-a"}

	out, err := run("E2E-CODE\n", append([]string{"login", "fake"}, base...)...)
	if err != nil {
		t.Fatalf("login failed: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{"==> Open: https://", "==> Code:", "login succeeded", "credentials pushed to the store"} {
		if !strings.Contains(out, want) {
			t.Errorf("login output missing %q:\n%s", want, out)
		}
	}
	credsA := filepath.Join(homeA, ".config", "fakecli", "credentials.json")
	dataA, err := os.ReadFile(credsA)
	if err != nil {
		t.Fatalf("credentials not written in home A: %v", err)
	}
	if !strings.Contains(string(dataA), "E2E-CODE") {
		t.Errorf("pasted code did not reach the CLI: %s", dataA)
	}

	out, err = run("", append([]string{"store", "list"}, base...)...)
	if err != nil {
		t.Fatalf("store list failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "fake") {
		t.Errorf("store list missing fake harness:\n%s", out)
	}

	// Second workspace: same user, different instance and home.
	t.Setenv("HOME", homeB)
	t.Setenv("XDG_STATE_HOME", filepath.Join(homeB, ".local", "state"))
	// Re-register adapters against the new home.
	adapter.ResetRegistry()
	adaptersRegistered = false
	baseB := []string{"--store", "file", "--store-path", storeRoot, "--instance", "ws-b"}
	out, err = run("", append([]string{"store", "restore"}, baseB...)...)
	if err != nil {
		t.Fatalf("restore failed: %v\n%s", err, out)
	}
	dataB, err := os.ReadFile(filepath.Join(homeB, ".config", "fakecli", "credentials.json"))
	if err != nil {
		t.Fatalf("credentials not restored in home B: %v", err)
	}
	if string(dataB) != string(dataA) {
		t.Errorf("restored credentials differ from original")
	}

	out, err = run("", append([]string{"status"}, baseB...)...)
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "fake") || !strings.Contains(out, "store") {
		t.Errorf("status does not show fake as store-managed:\n%s", out)
	}
}

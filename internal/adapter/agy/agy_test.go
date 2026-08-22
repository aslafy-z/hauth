package agy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
)

func TestLoginDetector(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/agy-login.txt")
	if err != nil {
		t.Fatal(err)
	}
	a := New(common.Config{Home: t.TempDir(), StateDir: t.TempDir()})
	evs := common.ReplayTranscript(a.Methods()[0].Detect(), raw)
	var prompt, success bool
	for _, e := range evs {
		switch e.Kind {
		case adapter.EventPrompt:
			prompt = true
		case adapter.EventSuccess:
			success = true
		}
	}
	if !prompt || !success {
		t.Fatalf("prompt=%v success=%v events=%+v", prompt, success, evs)
	}
}

func TestGlobPathsProbe(t *testing.T) {
	cfg := common.Config{Home: t.TempDir(), StateDir: t.TempDir()}
	a := New(cfg)
	st, err := a.Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != adapter.SourceNone {
		t.Fatalf("source = %v", st.Source)
	}

	dir := filepath.Join(cfg.Home, ".gemini/antigravity-cli/tokens")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err = a.Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != adapter.SourceFileExternal {
		t.Fatalf("recursive glob missed nested file, source = %v", st.Source)
	}
}

// Real log lines captured from agy 1.1.17.
const (
	logAuthedKeyring = `I0821 16:52:42.827113 57 keyring.go:81] keyringAuth: loaded token, expiry=2026-08-14 14:53:02 +0200 CEST expired=true
I0821 16:52:43.102710 1 auth.go:147] ChainedAuth: authenticated via keyring (effective: keyring)
I0821 16:52:43.102768 1 server_oauth.go:190] applyAuthResult: email=demo@example.com, authMethod=consumer, quotaProject=
I0821 16:52:43.102783 1 server_oauth.go:195] OAuth: authenticated successfully as demo@example.com`
	logNotLoggedIn = `E0821 16:54:05.375324 38 errorreport.go:223] error getting token source: You are not logged into Antigravity.
E0821 16:54:05.375433 38 errorreport.go:223] failed to get load code assist response: error getting token source: You are not logged into Antigravity.`
)

func TestParseAuthLog(t *testing.T) {
	account, valid, keyring := parseAuthLog(logAuthedKeyring)
	if account != "demo@example.com" || valid != adapter.ValidityValid || !keyring {
		t.Fatalf("authed: account=%q valid=%v keyring=%v", account, valid, keyring)
	}
	account, valid, keyring = parseAuthLog(logNotLoggedIn)
	if account != "" || valid != adapter.ValidityInvalid || keyring {
		t.Fatalf("anon: account=%q valid=%v keyring=%v", account, valid, keyring)
	}
	if _, valid, _ = parseAuthLog("no auth lines at all"); valid != adapter.ValidityUnknown {
		t.Fatalf("empty log should stay unknown, got %v", valid)
	}
}

// fakeRunner writes canned log content to the --log-file argument, the way
// the real CLI does.
func fakeRunner(log string) common.Runner {
	return func(ctx context.Context, name string, args ...string) (string, string, int, error) {
		for i, arg := range args {
			if arg == "--log-file" && i+1 < len(args) {
				if err := os.WriteFile(args[i+1], []byte(log), 0o600); err != nil {
					return "", "", 1, err
				}
			}
		}
		return "ok\n", "", 0, nil
	}
}

func TestExpensiveProbe(t *testing.T) {
	cfg := common.Config{Home: t.TempDir(), StateDir: t.TempDir(), Runner: fakeRunner(logAuthedKeyring)}
	st, err := New(cfg).Probe(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Valid != adapter.ValidityValid || st.Account != "demo@example.com" {
		t.Fatalf("valid=%v account=%q", st.Valid, st.Account)
	}
	// Keyring-authenticated with no files: managed externally, not "none".
	if st.Source != adapter.SourceFileExternal {
		t.Fatalf("source = %v, want file-external", st.Source)
	}

	cfg.Runner = fakeRunner(logNotLoggedIn)
	st, err = New(cfg).Probe(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Valid != adapter.ValidityInvalid || st.Source != adapter.SourceNone {
		t.Fatalf("anon: valid=%v source=%v", st.Valid, st.Source)
	}
}

func TestAgyOnboardingGatesAutoAnswered(t *testing.T) {
	a := New(common.Config{Home: t.TempDir(), StateDir: t.TempDir()})
	d := a.Methods()[0].Detect()
	for _, line := range []string{
		"Choose your color scheme:",
		"Select login method:",
		"Do you trust the contents of this project?",
	} {
		found := false
		for _, ev := range d.Feed(line) {
			if ev.Respond == "\r" {
				found = true
			}
		}
		if !found {
			t.Fatalf("gate %q not auto-answered", line)
		}
	}
}

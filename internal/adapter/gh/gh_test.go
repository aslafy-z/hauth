package gh

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
)

func testCfg(t *testing.T) common.Config {
	t.Helper()
	return common.Config{Home: t.TempDir(), StateDir: t.TempDir()}
}

func writeHosts(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".config/gh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "github.com:\n    user: octocat\n    oauth_token: gho_examplevalueexamplevalue\n    git_protocol: https\n"
	if err := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceDetector(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/gh-device.txt")
	if err != nil {
		t.Fatal(err)
	}
	a := New(testCfg(t))
	evs := common.ReplayTranscript(a.Methods()[0].Detect(), raw)
	var code, url, prompt, success bool
	for _, e := range evs {
		switch e.Kind {
		case adapter.EventCode:
			code = e.Code == "90AB-CDEF"
		case adapter.EventURL:
			url = e.URL == "https://github.com/login/device"
		case adapter.EventPrompt:
			prompt = true
		case adapter.EventSuccess:
			success = true
		}
	}
	if !code || !url || !prompt || !success {
		t.Fatalf("code=%v url=%v prompt=%v success=%v events=%+v", code, url, prompt, success, evs)
	}
}

func TestProbeCheap(t *testing.T) {
	cfg := testCfg(t)
	writeHosts(t, cfg.Home)
	st, err := New(cfg).Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != adapter.SourceFileExternal {
		t.Fatalf("source = %v", st.Source)
	}
	if st.Account != "octocat" {
		t.Fatalf("account = %q", st.Account)
	}
	if st.Valid != adapter.ValidityUnknown {
		t.Fatalf("cheap probe must not decide validity, got %v", st.Valid)
	}
}

func TestProbeFull(t *testing.T) {
	cfg := testCfg(t)
	writeHosts(t, cfg.Home)
	cfg.Runner = func(ctx context.Context, name string, args ...string) (string, string, int, error) {
		out := "github.com\n  ✓ Logged in to github.com account octocat (keyring)\n"
		return "", out, 0, nil
	}
	st, err := New(cfg).Probe(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Valid != adapter.ValidityValid || st.Account != "octocat" {
		t.Fatalf("valid=%v account=%q", st.Valid, st.Account)
	}
}

func TestLogoutEnvManaged(t *testing.T) {
	err := New(testCfg(t)).Logout(context.Background(), adapter.SourceEnv)
	if err == nil {
		t.Fatal("env-managed logout must fail with an explanation")
	}
}

func TestLogoutRemovesFiles(t *testing.T) {
	cfg := testCfg(t)
	writeHosts(t, cfg.Home)
	cfg.Runner = func(ctx context.Context, name string, args ...string) (string, string, int, error) {
		return "", "", 0, nil
	}
	if err := New(cfg).Logout(context.Background(), adapter.SourceFileExternal); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Home, ".config/gh/hosts.yml")); !os.IsNotExist(err) {
		t.Fatal("hosts.yml still present after logout")
	}
}

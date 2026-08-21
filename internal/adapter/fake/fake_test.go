package fake

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
)

func cliPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../../testdata/fakecli/fakecli")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoginTranscript(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/fake-login.txt")
	if err != nil {
		t.Fatal(err)
	}
	a := New(common.Config{Home: t.TempDir(), StateDir: t.TempDir()}, cliPath(t))
	evs := common.ReplayTranscript(a.Methods()[0].Detect(), raw)
	kinds := map[adapter.EventKind]bool{}
	for _, e := range evs {
		kinds[e.Kind] = true
	}
	for _, k := range []adapter.EventKind{adapter.EventURL, adapter.EventCode, adapter.EventPrompt, adapter.EventSuccess} {
		if !kinds[k] {
			t.Errorf("missing %s event: %+v", k, evs)
		}
	}
}

func TestStatusAndLogoutViaCLI(t *testing.T) {
	home := t.TempDir()
	// fakecli reads $HOME; ExecRunner inherits the test process env.
	t.Setenv("HOME", home)
	cfg := common.Config{Home: home, StateDir: t.TempDir()}
	a := New(cfg, cliPath(t))

	dir := filepath.Join(home, ".config/fakecli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cred := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(cred, []byte(`{"token":"fake-x","account":"demo@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := a.Probe(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Valid != adapter.ValidityValid || st.Account != "demo@example.com" {
		t.Fatalf("valid=%v account=%q", st.Valid, st.Account)
	}

	if err := a.Logout(context.Background(), adapter.SourceFileExternal); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cred); !os.IsNotExist(err) {
		t.Fatal("credentials still present after logout")
	}
}

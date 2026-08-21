package codex

import (
	"context"
	"encoding/base64"
	"fmt"
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

func writeAuth(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"dev@example.com"}`))
	jwt := "eyJhbGciOiJSUzI1NiJ9." + payload + ".sig"
	content := fmt.Sprintf(`{"tokens":{"id_token":"%s"}}`, jwt)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceDetector(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/codex-device.txt")
	if err != nil {
		t.Fatal(err)
	}
	a := New(testCfg(t))
	evs := common.ReplayTranscript(a.Methods()[0].Detect(), raw)
	var url, code, success bool
	for _, e := range evs {
		switch e.Kind {
		case adapter.EventURL:
			url = e.URL == "https://auth.openai.com/codex/device"
		case adapter.EventCode:
			code = e.Code == "4NEK-22H7V"
		case adapter.EventSuccess:
			success = true
		}
	}
	if !url || !code || !success {
		t.Fatalf("url=%v code=%v success=%v events=%+v", url, code, success, evs)
	}
}

func TestProbeAccountFromJWT(t *testing.T) {
	cfg := testCfg(t)
	writeAuth(t, cfg.Home)
	st, err := New(cfg).Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != adapter.SourceFileExternal || st.Account != "dev@example.com" {
		t.Fatalf("source=%v account=%q", st.Source, st.Account)
	}
}

func TestProbeFullStatus(t *testing.T) {
	cfg := testCfg(t)
	writeAuth(t, cfg.Home)
	cfg.Runner = func(ctx context.Context, name string, args ...string) (string, string, int, error) {
		return "Logged in using ChatGPT\n", "", 0, nil
	}
	st, err := New(cfg).Probe(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Valid != adapter.ValidityValid {
		t.Fatalf("valid = %v", st.Valid)
	}
}

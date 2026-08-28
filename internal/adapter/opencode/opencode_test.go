package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
)

func TestLoginTranscriptURLFallback(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/opencode-login.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Providers without explicit rules ride the generic URL fallback.
	a := New(common.Config{Home: t.TempDir(), StateDir: t.TempDir()})
	evs := common.ReplayTranscript(a.Methods()[0].Detect(), raw)
	var url bool
	for _, e := range evs {
		if e.Kind == adapter.EventURL && e.URL == "https://console.anthropic.com/settings/keys" {
			url = true
		}
	}
	if !url {
		t.Fatalf("no URL detected, events=%+v", evs)
	}
}

// The ChatGPT device flow shows a code the person must type into the
// provider's page; it has to surface as an EventCode for the kiosk.
func TestDeviceTranscriptSurfacesCode(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/opencode-device.txt")
	if err != nil {
		t.Fatal(err)
	}
	a := New(common.Config{Home: t.TempDir(), StateDir: t.TempDir()})
	evs := common.ReplayTranscript(a.Methods()[0].Detect(), raw)
	var url, code bool
	for _, e := range evs {
		switch e.Kind {
		case adapter.EventURL:
			url = e.URL == "https://auth.openai.com/codex/device"
		case adapter.EventCode:
			code = e.Code == "HF8T-HPD3H"
		}
	}
	if !url || !code {
		t.Fatalf("url=%v code=%v events=%+v", url, code, evs)
	}
}

func writeAuth(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".local/share/opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// auth.json holds one entry per provider; the probe surfaces which
// providers are logged in and, for OAuth, validity from the access expiry.
func TestProbeReadsProviders(t *testing.T) {
	future := time.Now().Add(time.Hour).UnixMilli()
	cfg := common.Config{Home: t.TempDir(), StateDir: t.TempDir()}
	writeAuth(t, cfg.Home, fmt.Sprintf(
		`{"openai":{"type":"oauth","refresh":"r","access":"a","expires":%d},"anthropic":{"type":"api","key":"sk-x"}}`, future))
	st, err := New(cfg).Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Account != "anthropic, openai" {
		t.Errorf("account = %q", st.Account)
	}
	if st.Valid != adapter.ValidityValid {
		t.Errorf("valid = %v, want valid (unexpired oauth)", st.Valid)
	}
}

func TestProbeExpiredOauthStaysUnknown(t *testing.T) {
	past := time.Now().Add(-time.Hour).UnixMilli()
	cfg := common.Config{Home: t.TempDir(), StateDir: t.TempDir()}
	writeAuth(t, cfg.Home, fmt.Sprintf(`{"openai":{"type":"oauth","refresh":"r","access":"a","expires":%d}}`, past))
	st, err := New(cfg).Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	// An expired access token still refreshes; never report invalid from
	// the file alone.
	if st.Valid != adapter.ValidityUnknown {
		t.Errorf("valid = %v, want unknown", st.Valid)
	}
	if st.Account != "openai" {
		t.Errorf("account = %q", st.Account)
	}
}

func TestProbePresence(t *testing.T) {
	cfg := common.Config{Home: t.TempDir(), StateDir: t.TempDir()}
	a := New(cfg)
	st, err := a.Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != adapter.SourceNone {
		t.Fatalf("source = %v, want none", st.Source)
	}

	dir := filepath.Join(cfg.Home, ".local/share/opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"anthropic":{"type":"api"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err = a.Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != adapter.SourceFileExternal || st.Valid != adapter.ValidityUnknown {
		t.Fatalf("source=%v valid=%v", st.Source, st.Valid)
	}
}

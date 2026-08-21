package opencode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
)

func TestLoginTranscriptURLFallback(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/opencode-login.txt")
	if err != nil {
		t.Fatal(err)
	}
	// The method declares no detector; sessions fall back to the generic
	// URL detector, mirrored here.
	evs := common.ReplayTranscript(adapter.NewLineDetector(), raw)
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

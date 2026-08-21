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
	var url, prompt, success bool
	for _, e := range evs {
		switch e.Kind {
		case adapter.EventURL:
			url = e.URL != ""
		case adapter.EventPrompt:
			prompt = true
		case adapter.EventSuccess:
			success = true
		}
	}
	if !url || !prompt || !success {
		t.Fatalf("url=%v prompt=%v success=%v events=%+v", url, prompt, success, evs)
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

	dir := filepath.Join(cfg.Home, ".config/agy/state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokens.json"), []byte(`{}`), 0o600); err != nil {
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

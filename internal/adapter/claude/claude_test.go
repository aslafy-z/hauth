package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
)

func testCfg(t *testing.T) common.Config {
	t.Helper()
	return common.Config{Home: t.TempDir(), StateDir: t.TempDir()}
}

func writeCreds(t *testing.T, home string, expiresAt time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-example","expiresAt":%d}}`, expiresAt.UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPasteDetector(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/claude-paste.txt")
	if err != nil {
		t.Fatal(err)
	}
	a := New(testCfg(t))
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

func TestProbeValidity(t *testing.T) {
	cfg := testCfg(t)
	writeCreds(t, cfg.Home, time.Now().Add(time.Hour))
	st, err := New(cfg).Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != adapter.SourceFileExternal || st.Valid != adapter.ValidityValid {
		t.Fatalf("source=%v valid=%v", st.Source, st.Valid)
	}

	writeCreds(t, cfg.Home, time.Now().Add(-time.Hour))
	st, err = New(cfg).Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Valid != adapter.ValidityInvalid {
		t.Fatalf("expired credentials reported %v", st.Valid)
	}
}

func TestSettingsFilter(t *testing.T) {
	in := map[string]any{
		"hasCompletedOnboarding": true,
		"oauthAccount":           map[string]any{"emailAddress": "user@example.com"},
		"userID":                 "u-123",
		"theme":                  "dark",
		"projects":               map[string]any{"/home/user/secret-project": map[string]any{"history": []string{"prompt"}}},
		"tipsHistory":            map[string]any{"tip": 1},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	a := New(testCfg(t)).(adapter.FileFilter)
	out, ok := a.FilterFile(".claude.json", data)
	if !ok {
		t.Fatal("settings file excluded entirely")
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"hasCompletedOnboarding", "oauthAccount", "userID", "theme"} {
		if _, ok := got[k]; !ok {
			t.Errorf("key %s dropped", k)
		}
	}
	for _, k := range []string{"projects", "tipsHistory"} {
		if _, ok := got[k]; ok {
			t.Errorf("key %s must not be synced", k)
		}
	}
}

func TestFilterPassesCredentialsThrough(t *testing.T) {
	a := New(testCfg(t)).(adapter.FileFilter)
	in := []byte(`{"claudeAiOauth":{}}`)
	out, ok := a.FilterFile(".claude/.credentials.json", in)
	if !ok || string(out) != string(in) {
		t.Fatal("credentials file must pass through unchanged")
	}
}

func TestOnboardingGatesAutoAnswered(t *testing.T) {
	d := pasteDetector()
	gates := []string{
		"Choose the text style that looks best with your terminal:",
		"Select login method:",
		"Press Enter to continue…",
		"Do you trust the files in this folder?",
	}
	for _, line := range gates {
		evs := d.Feed(line)
		if len(evs) != 1 || evs[0].Respond != "\r" {
			t.Fatalf("gate %q not auto-answered: %v", line, evs)
		}
	}
	// Redraws must not re-answer.
	if evs := d.Feed("Select login method:"); len(evs) != 0 {
		t.Fatalf("gate re-answered on redraw: %v", evs)
	}
}

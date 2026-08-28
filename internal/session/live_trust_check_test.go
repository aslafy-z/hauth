//go:build livecheck

package session_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/claude"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/session"
)

// TestLiveTrustGate runs the real claude CLI in a fresh HOME inside an
// untrusted folder and asserts the trust gate never blocks the login: the
// Prepare seeding must skip the dialog entirely and the ring must reach a
// login screen. Requires claude on PATH; guarded by the livecheck build tag
// so CI never runs it.
func TestLiveTrustGate(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"hasCompletedOnboarding": true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := claude.New(common.Config{Home: home, StateDir: t.TempDir()})
	m := a.Methods()[0]
	m.Cwd = proj

	s, err := session.Start(context.Background(), "claude", m, session.Options{
		Home:     home,
		StateDir: t.TempDir(),
		OnEvent: func(ev adapter.Event) {
			t.Logf("event: kind=%s respond=%q prompt=%q", ev.Kind, ev.Respond, ev.Prompt)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()

	trust := regexp.MustCompile(`I trust this folder`)
	login := regexp.MustCompile(`(?i)select login method|oauth/authorize|sign.?in`)
	deadline := time.After(45 * time.Second)
	for {
		out := string(s.Ring())
		if login.MatchString(out) {
			if trust.MatchString(out) {
				t.Fatalf("trust dialog appeared despite seeding; ring:\n%s", out)
			}
			return
		}
		if st, code := s.State(); st != session.StateRunning {
			t.Fatalf("session ended early: state=%s exit=%d; ring:\n%s", st, code, out)
		}
		select {
		case <-deadline:
			t.Fatalf("no login screen; ring:\n%s", out)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

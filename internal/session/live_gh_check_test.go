//go:build livecheck

package session_test

import (
	"context"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	ghadapter "github.com/aslafy-z/hauth/internal/adapter/gh"
	"github.com/aslafy-z/hauth/internal/session"
)

// TestLiveGhGates runs the real gh CLI with a fresh HOME and asserts the
// session answers its terminal queries and auto-answers both gates (git
// credentials, press-Enter) so the device URL and one-time code surface
// without any input. Requires gh on PATH and network; livecheck tag only.
func TestLiveGhGates(t *testing.T) {
	home := t.TempDir()
	a := ghadapter.New(common.Config{Home: home, StateDir: t.TempDir()})
	m := a.Methods()[0]

	var mu sync.Mutex
	var sawURL, sawCode bool
	s, err := session.Start(context.Background(), "gh", m, session.Options{
		Home:     home,
		StateDir: t.TempDir(),
		OnEvent: func(ev adapter.Event) {
			mu.Lock()
			defer mu.Unlock()
			switch ev.Kind {
			case adapter.EventURL:
				sawURL = sawURL || regexp.MustCompile(`github\.com/login/device`).MatchString(ev.URL)
			case adapter.EventCode:
				sawCode = ev.Code != ""
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()

	deadline := time.After(45 * time.Second)
	for {
		mu.Lock()
		urlOK, codeOK := sawURL, sawCode
		mu.Unlock()
		if urlOK && codeOK {
			return
		}
		if st, code := s.State(); st != session.StateRunning {
			t.Fatalf("session ended early: state=%s exit=%d; ring:\n%s", st, code, s.Ring())
		}
		select {
		case <-deadline:
			t.Fatalf("url=%v code=%v; ring:\n%s", urlOK, codeOK, s.Ring())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

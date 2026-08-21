package session_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/adapter/fake"
	"github.com/aslafy-z/hauth/internal/session"
)

func fakecliPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../testdata/fakecli/fakecli")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fakecli missing: %v", err)
	}
	return p
}

func TestSessionFakeLogin(t *testing.T) {
	home := t.TempDir()
	stateDir := t.TempDir()
	a := fake.New(common.Config{Home: home, StateDir: stateDir}, fakecliPath(t))
	m := a.Methods()[0]

	var mu sync.Mutex
	var events []adapter.Event
	prompt := make(chan struct{}, 1)
	s, err := session.Start(context.Background(), "fake", m, session.Options{
		Home:     home,
		StateDir: stateDir,
		OnEvent: func(e adapter.Event) {
			mu.Lock()
			events = append(events, e)
			mu.Unlock()
			if e.Kind == adapter.EventPrompt {
				select {
				case prompt <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-prompt:
	case <-time.After(15 * time.Second):
		t.Fatalf("no prompt detected; ring: %q", s.Ring())
	}
	if err := s.SendLine("CODE-1234"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("session did not finish; ring: %q", s.Ring())
	}
	state, code := s.State()
	if state != session.StateSucceeded || code != 0 {
		t.Fatalf("state=%s code=%d ring=%q", state, code, s.Ring())
	}

	cred := filepath.Join(home, ".config/fakecli/credentials.json")
	data, err := os.ReadFile(cred)
	if err != nil {
		t.Fatalf("credentials not written: %v", err)
	}
	var c struct {
		Token   string `json:"token"`
		Account string `json:"account"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Token != "fake-CODE-1234" {
		t.Fatalf("token = %q", c.Token)
	}

	mu.Lock()
	defer mu.Unlock()
	var urls, codes, successes int
	for _, e := range events {
		switch e.Kind {
		case adapter.EventURL:
			urls++
			if e.URL != "https://auth.example.com/device?user=demo" {
				t.Errorf("unexpected URL %q", e.URL)
			}
		case adapter.EventCode:
			codes++
			if e.Code != "CODE-1234" {
				t.Errorf("unexpected code %q", e.Code)
			}
		case adapter.EventSuccess:
			successes++
		}
	}
	// The shim and the output regex both see the same URL; dedupe must
	// collapse them into one event.
	if urls != 1 {
		t.Errorf("url events = %d, want 1", urls)
	}
	if codes != 1 || successes == 0 {
		t.Errorf("codes=%d successes=%d", codes, successes)
	}
	if !strings.Contains(string(s.Ring()), "Login successful") {
		t.Errorf("ring missing success line: %q", s.Ring())
	}

	// After a manual login the files exist but were not restored by hauth:
	// the source must resolve to file-external.
	st, err := a.Probe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != adapter.SourceFileExternal {
		t.Errorf("source = %v, want file-external", st.Source)
	}
	if st.Account != "demo@example.com" {
		t.Errorf("account = %q", st.Account)
	}
}

func TestSessionCancel(t *testing.T) {
	home := t.TempDir()
	m := adapter.Method{
		ID:   "sleep",
		Argv: []string{"sh", "-c", "sleep 30"},
	}
	s, err := session.Start(context.Background(), "fake", m, session.Options{Home: home, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s.Cancel()
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not finish the session")
	}
	if state, _ := s.State(); state != session.StateCanceled {
		t.Fatalf("state = %s, want canceled", state)
	}
}

func TestSessionSubscribeReplaysRing(t *testing.T) {
	home := t.TempDir()
	m := adapter.Method{ID: "echo", Argv: []string{"sh", "-c", "echo hello-ring"}}
	s, err := session.Start(context.Background(), "fake", m, session.Options{Home: home, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	<-s.Done()
	ch, cancel := s.Subscribe()
	defer cancel()
	var got []byte
	for chunk := range ch {
		got = append(got, chunk...)
	}
	if !strings.Contains(string(got), "hello-ring") {
		t.Fatalf("late subscriber missed ring: %q", got)
	}
}

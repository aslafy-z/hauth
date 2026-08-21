// Package app wires adapters, sessions, the syncer, and the kiosk together.
// It implements kiosk.Controller and owns the one-active-session-per-harness
// rule and the post-login store push.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/installer"
	"github.com/aslafy-z/hauth/internal/kiosk"
	"github.com/aslafy-z/hauth/internal/session"
	"github.com/aslafy-z/hauth/internal/source"
	"github.com/aslafy-z/hauth/internal/syncer"
)

// Config wires an App.
type Config struct {
	User     string
	Instance string
	Home     string
	StateDir string
	// Syncer is nil when no store is configured; logins then stay local.
	Syncer *syncer.Syncer
	Logf   func(format string, args ...any)
}

// App implements kiosk.Controller.
type App struct {
	cfg Config

	mu        sync.Mutex
	sessions  map[string]*session.Session // by session id
	byHarness map[string]string           // harness -> active session id
	status    map[string]cachedStatus

	subMu sync.Mutex
	subs  map[int]chan kiosk.Event
	nextS int
}

type cachedStatus struct {
	st        adapter.Status
	at        time.Time
	expensive bool
}

const (
	cheapStatusTTL     = 15 * time.Second
	expensiveStatusTTL = 5 * time.Minute
)

// New builds an App.
func New(cfg Config) *App {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return &App{
		cfg:       cfg,
		sessions:  map[string]*session.Session{},
		byHarness: map[string]string{},
		status:    map[string]cachedStatus{},
		subs:      map[int]chan kiosk.Event{},
	}
}

func (a *App) publish(ev kiosk.Event) {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	for _, ch := range a.subs {
		select {
		case ch <- ev:
		default: // slow subscriber: drop rather than block the app
		}
	}
}

// Subscribe implements kiosk.Controller.
func (a *App) Subscribe() (<-chan kiosk.Event, func()) {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	id := a.nextS
	a.nextS++
	ch := make(chan kiosk.Event, 64)
	a.subs[id] = ch
	return ch, func() {
		a.subMu.Lock()
		defer a.subMu.Unlock()
		if c, ok := a.subs[id]; ok {
			delete(a.subs, id)
			close(c)
		}
	}
}

func (a *App) probe(ctx context.Context, ad adapter.Adapter, cheap bool) adapter.Status {
	name := ad.Name()
	a.mu.Lock()
	c, ok := a.status[name]
	a.mu.Unlock()
	ttl := cheapStatusTTL
	if ok && c.expensive {
		ttl = expensiveStatusTTL
	}
	if ok && cheap && time.Since(c.at) < ttl {
		return c.st
	}
	st, err := ad.Probe(ctx, cheap)
	if err != nil {
		a.cfg.Logf("probe %s: %v", name, err)
		src, detail := source.Resolve(ad, a.cfg.Home, a.cfg.StateDir)
		st = adapter.Status{Source: src, Detail: detail, Valid: adapter.ValidityUnknown, CheckedAt: time.Now()}
	}
	if st.CheckedAt.IsZero() {
		st.CheckedAt = time.Now()
	}
	a.mu.Lock()
	a.status[name] = cachedStatus{st: st, at: time.Now(), expensive: !cheap}
	a.mu.Unlock()
	return st
}

func (a *App) invalidate(harness string) {
	a.mu.Lock()
	delete(a.status, harness)
	a.mu.Unlock()
}

func (a *App) view(ctx context.Context, ad adapter.Adapter, st adapter.Status) kiosk.HarnessView {
	name := ad.Name()
	v := kiosk.HarnessView{
		Name:      name,
		Source:    st.Source.String(),
		Detail:    st.Detail,
		Validity:  st.Valid.String(),
		Account:   st.Account,
		CheckedAt: st.CheckedAt,
		Refresh:   ad.Refresh().String(),
	}
	for _, m := range ad.Methods() {
		v.Methods = append(v.Methods, kiosk.MethodView{ID: m.ID, Title: m.Title, Input: m.Input.String()})
	}
	// A cheap probe that cannot decide validity means a real check costs a
	// network call (or worse); surface an explicit button instead.
	v.CheckIsExpensive = st.Valid == adapter.ValidityUnknown

	if installer.Known(name) {
		v.Installed = installer.Installed(name)
		v.CanInstall = !v.Installed && installer.Available()
	} else {
		// Unmapped harnesses (e.g. the fake adapter) must not be blocked
		// on installation state.
		v.Installed = true
	}

	switch st.Source {
	case adapter.SourceEnv:
		v.CanLogout = false
		v.LogoutReason = fmt.Sprintf("set by the environment (%s); remove it from your workspace config", strings.TrimPrefix(st.Detail, "env "))
		v.CanLogin = !ad.EnvOverridesFiles()
		if !v.CanLogin {
			v.LoginReason = "the environment variable wins over files for this CLI"
		}
	case adapter.SourceNone:
		v.CanLogin = true
	case adapter.SourceStore, adapter.SourceFileExternal:
		v.CanLogin = true
		v.CanLogout = true
	}
	if st.Source == adapter.SourceFileExternal && a.cfg.Syncer != nil {
		v.CanAdopt = true
	}
	if !v.Installed {
		v.CanLogin = false
		v.LoginReason = "CLI not installed"
	}

	if a.cfg.Syncer != nil {
		for _, ss := range a.cfg.Syncer.Status(ctx) {
			if ss.Harness == name && !ss.WeHold {
				v.LeaseHolder = ss.LeaseHolder
			}
		}
	}

	a.mu.Lock()
	if id, ok := a.byHarness[name]; ok {
		if s := a.sessions[id]; s != nil {
			state, _ := s.State()
			v.SessionID = id
			v.SessionState = string(state)
		}
	}
	a.mu.Unlock()
	return v
}

// Statuses implements kiosk.Controller with cheap probes only.
func (a *App) Statuses(ctx context.Context) []kiosk.HarnessView {
	var out []kiosk.HarnessView
	for _, ad := range adapter.All() {
		out = append(out, a.view(ctx, ad, a.probe(ctx, ad, true)))
	}
	return out
}

// Check implements kiosk.Controller: an explicit, possibly expensive probe.
func (a *App) Check(ctx context.Context, harness string) (kiosk.HarnessView, error) {
	ad, ok := adapter.Get(harness)
	if !ok {
		return kiosk.HarnessView{}, fmt.Errorf("unknown harness %q", harness)
	}
	st := a.probe(ctx, ad, false)
	return a.view(ctx, ad, st), nil
}

func newSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func findMethod(ad adapter.Adapter, id string) (adapter.Method, error) {
	methods := ad.Methods()
	if len(methods) == 0 {
		return adapter.Method{}, fmt.Errorf("harness %q has no login methods", ad.Name())
	}
	if id == "" {
		return methods[0], nil
	}
	for _, m := range methods {
		if m.ID == id {
			return m, nil
		}
	}
	return adapter.Method{}, fmt.Errorf("harness %q has no method %q", ad.Name(), id)
}

// StartLogin implements kiosk.Controller. Starting a new session for a
// harness cancels the previous one. For rotating harnesses backed by a
// store, the lease is acquired first so the login's result may be pushed.
func (a *App) StartLogin(ctx context.Context, harness, method string) (string, error) {
	ad, ok := adapter.Get(harness)
	if !ok {
		return "", fmt.Errorf("unknown harness %q", harness)
	}
	m, err := findMethod(ad, method)
	if err != nil {
		return "", err
	}
	if a.cfg.Syncer != nil && ad.Refresh() == adapter.RefreshRotating {
		held, holder, err := a.cfg.Syncer.Leases().EnsureHolder(ctx, harness)
		if err == nil && !held {
			return "", fmt.Errorf("credentials for %s are owned by %s; take-over happens automatically once its lease expires", harness, holder)
		}
	}

	a.mu.Lock()
	if oldID, ok := a.byHarness[harness]; ok {
		if old := a.sessions[oldID]; old != nil {
			old.Cancel()
		}
		delete(a.sessions, oldID)
		delete(a.byHarness, harness)
	}
	a.mu.Unlock()

	id := newSessionID()
	// Adopt as soon as the detector sees success, not only at process
	// exit: REPL-style CLIs keep running after logging in, and the user
	// must never have to adopt a login that hauth itself ran.
	var adoptOnce sync.Once
	adopt := func() {
		adoptOnce.Do(func() {
			if a.cfg.Syncer == nil {
				return
			}
			if err := a.cfg.Syncer.Adopt(context.Background(), harness); err != nil {
				a.cfg.Logf("push after login %s: %v", harness, err)
			}
			a.invalidate(harness)
			a.publish(kiosk.Event{Type: "status_changed", Harness: harness})
		})
	}
	opts := session.Options{
		Home:     a.cfg.Home,
		StateDir: a.cfg.StateDir,
		OnEvent: func(ev adapter.Event) {
			out := kiosk.Event{Harness: harness, SessionID: id, URL: ev.URL, Code: ev.Code, Prompt: ev.Prompt}
			switch ev.Kind {
			case adapter.EventURL:
				out.Type = "session_url"
			case adapter.EventCode:
				out.Type = "session_code"
			case adapter.EventPrompt:
				out.Type = "session_prompt"
			case adapter.EventSuccess:
				// Give the CLI a moment to finish writing credential files.
				time.AfterFunc(2*time.Second, adopt)
				return
			default:
				return // failure surfaces via session_state
			}
			a.publish(out)
		},
	}
	// The request context dies when the login POST returns; the session
	// must outlive it.
	s, err := session.Start(context.WithoutCancel(ctx), harness, m, opts)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.sessions[id] = s
	a.byHarness[harness] = id
	a.mu.Unlock()
	a.publish(kiosk.Event{Type: "session_state", Harness: harness, SessionID: id, State: string(session.StateRunning)})

	go func() {
		<-s.Done()
		state, code := s.State()
		a.cfg.Logf("session %s (%s/%s) finished: %s (exit %d)", id, harness, m.ID, state, code)
		if state == session.StateSucceeded {
			// hauth ran this login itself, so recording and pushing the
			// result is not silent adoption. No-op when the success event
			// already adopted.
			adopt()
		}
		a.invalidate(harness)
		a.publish(kiosk.Event{Type: "session_state", Harness: harness, SessionID: id, State: string(state)})
		a.publish(kiosk.Event{Type: "status_changed", Harness: harness})
	}()
	return id, nil
}

// SendInput implements kiosk.Controller.
func (a *App) SendInput(ctx context.Context, sessionID, text string) error {
	_ = ctx
	a.mu.Lock()
	s := a.sessions[sessionID]
	a.mu.Unlock()
	if s == nil {
		return errors.New("unknown session")
	}
	return s.SendLine(text)
}

// CancelSession implements kiosk.Controller.
func (a *App) CancelSession(ctx context.Context, sessionID string) error {
	_ = ctx
	a.mu.Lock()
	s := a.sessions[sessionID]
	a.mu.Unlock()
	if s == nil {
		return errors.New("unknown session")
	}
	s.Cancel()
	return nil
}

// Logout implements kiosk.Controller. Order: CLI-native logout first (the
// adapter does that), then store cleanup when the credentials were ours.
func (a *App) Logout(ctx context.Context, harness string) error {
	ad, ok := adapter.Get(harness)
	if !ok {
		return fmt.Errorf("unknown harness %q", harness)
	}
	src, _ := source.Resolve(ad, a.cfg.Home, a.cfg.StateDir)
	if src == adapter.SourceEnv {
		return fmt.Errorf("%s is managed by the environment; hauth cannot unset it", harness)
	}
	if err := ad.Logout(ctx, src); err != nil {
		return err
	}
	if src == adapter.SourceStore && a.cfg.Syncer != nil {
		if err := a.cfg.Syncer.DeleteHarness(ctx, harness); err != nil {
			return fmt.Errorf("local logout done, store cleanup failed: %w", err)
		}
	}
	source.ClearRestored(a.cfg.StateDir, harness)
	a.invalidate(harness)
	a.publish(kiosk.Event{Type: "status_changed", Harness: harness})
	return nil
}

// Relogin implements kiosk.Controller: logout, then the default method, as
// one operation so the user never ends up half logged out.
func (a *App) Relogin(ctx context.Context, harness string) (string, error) {
	if err := a.Logout(ctx, harness); err != nil {
		return "", err
	}
	return a.StartLogin(ctx, harness, "")
}

// Install implements kiosk.Controller: run the harness CLI installation in
// a session so the UI streams progress like a login flow. It reuses the
// one-session-per-harness slot.
func (a *App) Install(ctx context.Context, harness string) (string, error) {
	if _, ok := adapter.Get(harness); !ok {
		return "", fmt.Errorf("unknown harness %q", harness)
	}
	if !installer.Known(harness) {
		return "", fmt.Errorf("no installer for harness %q", harness)
	}
	// Run our own `install` subcommand in the pty so the installer's
	// progress streams through the session like a login flow.
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate hauth binary: %w", err)
	}
	argv := []string{self, "install", harness}
	m := adapter.Method{
		ID:      "install",
		Title:   "Install",
		Argv:    argv,
		Input:   adapter.InputNone,
		Timeout: 10 * time.Minute,
	}

	a.mu.Lock()
	if oldID, ok := a.byHarness[harness]; ok {
		if old := a.sessions[oldID]; old != nil {
			old.Cancel()
		}
		delete(a.sessions, oldID)
		delete(a.byHarness, harness)
	}
	a.mu.Unlock()

	id := newSessionID()
	s, err := session.Start(context.WithoutCancel(ctx), harness, m, session.Options{
		Home:     a.cfg.Home,
		StateDir: a.cfg.StateDir,
	})
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.sessions[id] = s
	a.byHarness[harness] = id
	a.mu.Unlock()
	a.publish(kiosk.Event{Type: "session_state", Harness: harness, SessionID: id, State: string(session.StateRunning)})

	go func() {
		<-s.Done()
		state, code := s.State()
		a.cfg.Logf("install session %s (%s) finished: %s (exit %d)", id, harness, state, code)
		a.invalidate(harness)
		a.publish(kiosk.Event{Type: "session_state", Harness: harness, SessionID: id, State: string(state)})
		a.publish(kiosk.Event{Type: "status_changed", Harness: harness})
	}()
	return id, nil
}

// Adopt implements kiosk.Controller: explicit adoption of file-external
// credentials into the store. Never automatic.
func (a *App) Adopt(ctx context.Context, harness string) error {
	if a.cfg.Syncer == nil {
		return errors.New("no store configured")
	}
	if _, ok := adapter.Get(harness); !ok {
		return fmt.Errorf("unknown harness %q", harness)
	}
	if err := a.cfg.Syncer.Adopt(ctx, harness); err != nil {
		return err
	}
	a.invalidate(harness)
	a.publish(kiosk.Event{Type: "status_changed", Harness: harness})
	return nil
}

type attached struct{ s *session.Session }

func (w attached) Subscribe() (<-chan []byte, func()) { return w.s.Subscribe() }
func (w attached) Write(p []byte) (int, error) {
	if err := w.s.Write(string(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (w attached) SendLine(text string) error     { return w.s.SendLine(text) }
func (w attached) Resize(rows, cols uint16) error { return w.s.Resize(rows, cols) }
func (w attached) Done() <-chan struct{}          { return w.s.Done() }

// Attach implements kiosk.Controller.
func (a *App) Attach(sessionID string) (kiosk.AttachedSession, bool) {
	a.mu.Lock()
	s := a.sessions[sessionID]
	a.mu.Unlock()
	if s == nil {
		return nil, false
	}
	return attached{s: s}, true
}

// RunAgent restores from the store, starts the sync loop, and serves the
// kiosk until ctx is canceled. With noUI, only the sync loop runs. A light
// poller publishes status_changed when background file changes shift a
// harness's resolved source.
func (a *App) RunAgent(ctx context.Context, listen string, allowedOrigins []string, noUI bool) error {
	errCh := make(chan error, 2)
	if a.cfg.Syncer != nil {
		go func() { errCh <- a.cfg.Syncer.Run(ctx) }()
	} else if noUI {
		return errors.New("nothing to do: no store configured and no UI requested")
	}
	if !noUI {
		srv, err := kiosk.New(a, kiosk.Options{Listen: listen, AllowedOrigins: allowedOrigins})
		if err != nil {
			return err
		}
		a.cfg.Logf("kiosk listening on http://%s", srv.Addr())
		go func() { errCh <- srv.Start(ctx) }()
		go a.pollStatus(ctx)
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func (a *App) pollStatus(ctx context.Context) {
	last := map[string]string{}
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, ad := range adapter.All() {
				src, detail := source.Resolve(ad, a.cfg.Home, a.cfg.StateDir)
				key := src.String() + "|" + detail
				if prev, ok := last[ad.Name()]; ok && prev != key {
					a.invalidate(ad.Name())
					a.publish(kiosk.Event{Type: "status_changed", Harness: ad.Name()})
				}
				last[ad.Name()] = key
			}
		}
	}
}

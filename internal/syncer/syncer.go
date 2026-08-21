// Package syncer orchestrates restore, watch, and push between the local
// home directory and the store. It is the only place that combines the
// adapter registry, bundles, source detection, and leases.
//
// The push rules encode the "never adopt silently" decision: hauth pushes a
// harness only when a restored-state record exists for it locally (hauth
// itself logged in or restored earlier). Credentials that appeared by any
// other means stay file-external until an explicit Adopt.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/bundle"
	"github.com/aslafy-z/hauth/internal/lease"
	"github.com/aslafy-z/hauth/internal/source"
	"github.com/aslafy-z/hauth/internal/store"
)

// Config wires a Syncer.
type Config struct {
	Store    store.Store
	User     string
	Instance string
	Home     string
	StateDir string
	// TTL is the lease duration; <= 0 means lease.DefaultTTL.
	TTL time.Duration
	// Debounce delays a push after a file event; <= 0 means 2s.
	Debounce time.Duration
	// Logf receives progress lines; nil discards them.
	Logf func(format string, args ...any)
}

// SyncStatus describes one harness's sync state for the kiosk and CLI.
type SyncStatus struct {
	Harness     string                `json:"harness"`
	Policy      adapter.RefreshPolicy `json:"-"`
	PolicyName  string                `json:"policy"`
	LeaseHolder string                `json:"lease_holder,omitempty"`
	WeHold      bool                  `json:"we_hold"`
	LastPush    time.Time             `json:"last_push,omitzero"`
	LastError   string                `json:"last_error,omitempty"`
}

type harnessState struct {
	lastPush time.Time
	lastErr  string
}

// Syncer implements the agent's store side.
type Syncer struct {
	cfg    Config
	leases *lease.Manager

	mu    sync.Mutex
	state map[string]*harnessState
}

// New builds a Syncer.
func New(cfg Config) *Syncer {
	if cfg.Debounce <= 0 {
		cfg.Debounce = 2 * time.Second
	}
	return &Syncer{
		cfg:    cfg,
		leases: lease.NewManager(cfg.Store, cfg.User, cfg.Instance, cfg.TTL),
		state:  map[string]*harnessState{},
	}
}

// Leases exposes the lease manager for callers that need holder decisions.
func (s *Syncer) Leases() *lease.Manager { return s.leases }

func (s *Syncer) logf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
	}
}

func (s *Syncer) hstate(harness string) *harnessState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[harness]
	if !ok {
		st = &harnessState{}
		s.state[harness] = st
	}
	return st
}

func (s *Syncer) recordErr(harness string, err error) {
	st := s.hstate(harness)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		st.lastErr = err.Error()
	} else {
		st.lastErr = ""
	}
}

func (s *Syncer) recordPush(harness string) {
	st := s.hstate(harness)
	s.mu.Lock()
	defer s.mu.Unlock()
	st.lastPush = time.Now()
	st.lastErr = ""
}

func filterFor(a adapter.Adapter) bundle.Filter {
	ff, ok := a.(adapter.FileFilter)
	if !ok {
		return nil
	}
	return func(rel string, content []byte) ([]byte, bool) {
		return ff.FilterFile(rel, content)
	}
}

// sameFiles reports whether two manifests describe identical content.
func sameFiles(a, b bundle.Manifest) bool {
	if len(a.Files) != len(b.Files) {
		return false
	}
	hashes := make(map[string]string, len(a.Files))
	for _, f := range a.Files {
		hashes[f.Path] = f.SHA256
	}
	for _, f := range b.Files {
		if hashes[f.Path] != f.SHA256 {
			return false
		}
	}
	return true
}

// RestoreAll restores every syncable harness that has a stored bundle and no
// externally managed local credentials. Per-harness failures are joined, not
// fatal for the rest.
func (s *Syncer) RestoreAll(ctx context.Context) error {
	var errs []error
	for _, a := range adapter.All() {
		if a.Refresh() == adapter.RefreshNone {
			continue
		}
		if err := s.restoreOne(ctx, a); err != nil {
			s.recordErr(a.Name(), err)
			errs = append(errs, fmt.Errorf("%s: %w", a.Name(), err))
		}
	}
	return errors.Join(errs...)
}

func (s *Syncer) restoreOne(ctx context.Context, a adapter.Adapter) error {
	b, rev, _, err := s.cfg.Store.Get(ctx, s.cfg.User, a.Name())
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if b.Manifest.IdentityMatchesLocal(s.cfg.Home, a.IdentityPaths()) {
		// Local files already are the stored bundle; refresh the record so
		// they resolve as store-managed.
		return source.SaveRestored(s.cfg.StateDir, b.Manifest)
	}
	src, detail := source.Resolve(a, s.cfg.Home, s.cfg.StateDir)
	switch src {
	case adapter.SourceEnv, adapter.SourceFileExternal:
		s.logf("restore %s: skipped, %s", a.Name(), detail)
		return nil
	}
	if err := bundle.Unpack(b, s.cfg.Home); err != nil {
		return fmt.Errorf("unpack rev %s: %w", rev, err)
	}
	if err := source.SaveRestored(s.cfg.StateDir, b.Manifest); err != nil {
		return err
	}
	s.logf("restore %s: rev %s from %s", a.Name(), rev, b.Manifest.Instance)
	return nil
}

// PushHarness packs and pushes one harness. force bypasses the lease and the
// external-source guard (used by login and Adopt). It returns whether a push
// happened.
func (s *Syncer) PushHarness(ctx context.Context, harness string, force bool) (bool, error) {
	pushed, err := s.pushHarness(ctx, harness, force)
	if err != nil {
		s.recordErr(harness, err)
	}
	return pushed, err
}

func (s *Syncer) pushHarness(ctx context.Context, harness string, force bool) (bool, error) {
	a, ok := adapter.Get(harness)
	if !ok {
		return false, fmt.Errorf("unknown harness %q", harness)
	}
	if a.Refresh() == adapter.RefreshNone {
		return false, nil
	}
	if !force {
		for _, spec := range a.EnvAuth() {
			if os.Getenv(spec.Var) != "" {
				return false, nil
			}
		}
		if _, ok := source.LoadRestored(s.cfg.StateDir, harness); !ok {
			// Never adopt external files implicitly.
			return false, nil
		}
		allowed, err := s.leases.ShouldPush(ctx, harness, a.Refresh())
		if err != nil {
			return false, err
		}
		if !allowed {
			return false, nil
		}
	}
	b, err := bundle.Pack(s.cfg.Home, harness, s.cfg.Instance, a.Paths(), a.IdentityPaths(), filterFor(a))
	if err != nil {
		return false, err
	}
	if len(b.Manifest.Files) == 0 {
		return false, nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		cur, rev, _, err := s.cfg.Store.Get(ctx, s.cfg.User, harness)
		switch {
		case errors.Is(err, store.ErrNotFound):
			rev = ""
		case err != nil:
			return false, err
		default:
			if sameFiles(cur.Manifest, b.Manifest) {
				return false, source.SaveRestored(s.cfg.StateDir, cur.Manifest)
			}
		}
		if _, err := s.cfg.Store.Put(ctx, s.cfg.User, harness, b, rev); err != nil {
			if errors.Is(err, store.ErrCASConflict) {
				continue
			}
			return false, err
		}
		if err := source.SaveRestored(s.cfg.StateDir, b.Manifest); err != nil {
			return true, err
		}
		s.recordPush(harness)
		s.logf("push %s: %d files", harness, len(b.Manifest.Files))
		return true, nil
	}
	return false, store.ErrCASConflict
}

// Adopt explicitly takes file-external credentials into the store.
func (s *Syncer) Adopt(ctx context.Context, harness string) error {
	pushed, err := s.PushHarness(ctx, harness, true)
	if err != nil {
		return err
	}
	if !pushed {
		// Content already matched the store; PushHarness recorded it as
		// restored, which is exactly what adoption means here.
		s.logf("adopt %s: store already current", harness)
	}
	return nil
}

// DeleteHarness removes the stored bundle, forgets the local restored
// record, and releases any lease we hold.
func (s *Syncer) DeleteHarness(ctx context.Context, harness string) error {
	if err := s.cfg.Store.Delete(ctx, s.cfg.User, harness); err != nil {
		return err
	}
	source.ClearRestored(s.cfg.StateDir, harness)
	return s.leases.Release(ctx, harness)
}

// Status summarizes all registered syncable harnesses.
func (s *Syncer) Status(ctx context.Context) []SyncStatus {
	var out []SyncStatus
	now := time.Now()
	for _, a := range adapter.All() {
		if a.Refresh() == adapter.RefreshNone {
			continue
		}
		st := s.hstate(a.Name())
		s.mu.Lock()
		item := SyncStatus{
			Harness:    a.Name(),
			Policy:     a.Refresh(),
			PolicyName: a.Refresh().String(),
			LastPush:   st.lastPush,
			LastError:  st.lastErr,
		}
		s.mu.Unlock()
		if _, _, l, err := s.cfg.Store.Get(ctx, s.cfg.User, a.Name()); err == nil && l.Live(now) {
			item.LeaseHolder = l.Holder
			item.WeHold = l.Holder == s.cfg.Instance
		}
		out = append(out, item)
	}
	return out
}

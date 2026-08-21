package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/bundle"
)

// rescanInterval bounds how long a newly created credential directory can go
// unwatched; fsnotify cannot watch directories that do not exist yet.
const rescanInterval = 30 * time.Second

// staticPrefix returns the pattern's leading path segments that contain no
// glob metacharacters.
func staticPrefix(pattern string) string {
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	var kept []string
	for _, p := range parts {
		if strings.ContainsAny(p, "*?[") {
			break
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, "/")
}

// watchDirs lists the directories that currently exist and could contain
// files matched by adapter paths, including subdirectories under recursive
// patterns.
func (s *Syncer) watchDirs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return
		}
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	for _, a := range adapter.All() {
		if a.Refresh() == adapter.RefreshNone {
			continue
		}
		for _, p := range a.Paths() {
			prefix := staticPrefix(p)
			abs := filepath.Join(s.cfg.Home, filepath.FromSlash(prefix))
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				add(abs)
				if strings.Contains(p, "**") {
					_ = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
						if err == nil && d.IsDir() {
							add(path)
						}
						return nil
					})
				}
			} else {
				add(filepath.Dir(abs))
			}
		}
	}
	return out
}

// Watch reports harnesses whose credential files changed, debounced per
// harness. It blocks until ctx is done.
func (s *Syncer) Watch(ctx context.Context, onChange func(harness string)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	watched := map[string]bool{}
	addAll := func() {
		for _, dir := range s.watchDirs() {
			if !watched[dir] {
				if err := w.Add(dir); err == nil {
					watched[dir] = true
				}
			}
		}
	}
	addAll()

	var mu sync.Mutex
	timers := map[string]*time.Timer{}
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, t := range timers {
			t.Stop()
		}
	}()
	trigger := func(harness string) {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := timers[harness]; ok {
			t.Reset(s.cfg.Debounce)
			return
		}
		timers[harness] = time.AfterFunc(s.cfg.Debounce, func() {
			mu.Lock()
			delete(timers, harness)
			mu.Unlock()
			if ctx.Err() == nil {
				onChange(harness)
			}
		})
	}

	rescan := time.NewTicker(rescanInterval)
	defer rescan.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-rescan.C:
			addAll()
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					addAll()
					continue
				}
			}
			rel, err := filepath.Rel(s.cfg.Home, ev.Name)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			rel = filepath.ToSlash(rel)
			for _, a := range adapter.All() {
				if a.Refresh() == adapter.RefreshNone {
					continue
				}
				if bundle.MatchesPattern(rel, a.Paths()) {
					trigger(a.Name())
				}
			}
		case <-w.Errors:
			// Watcher errors are transient in practice; the periodic
			// rescan restores coverage.
		}
	}
}

// Run is the agent main loop: restore everything, hold leases for Rotating
// harnesses where possible, and push local changes as they happen.
func (s *Syncer) Run(ctx context.Context) error {
	if err := s.RestoreAll(ctx); err != nil {
		s.logf("restore: %v", err)
	}
	var wg sync.WaitGroup
	for _, a := range adapter.All() {
		if a.Refresh() != adapter.RefreshRotating {
			continue
		}
		harness := a.Name()
		held, holder, err := s.leases.EnsureHolder(ctx, harness)
		if err != nil {
			s.logf("lease %s: %v", harness, err)
			continue
		}
		if !held {
			s.logf("lease %s: held by %s, refresh disabled here", harness, holder)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.leases.RunRenewal(ctx, harness)
		}()
	}
	err := s.Watch(ctx, func(harness string) {
		pctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.PushHarness(pctx, harness, false); err != nil {
			s.logf("push %s: %v", harness, err)
		}
	})
	wg.Wait()
	return err
}

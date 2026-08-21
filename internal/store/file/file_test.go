package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aslafy-z/hauth/internal/bundle"
	"github.com/aslafy-z/hauth/internal/store"
)

func testBundle(t *testing.T, harness, content string) bundle.Bundle {
	t.Helper()
	home := t.TempDir()
	rel := filepath.Join(".config", harness, "creds.json")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(home, rel)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, rel), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	pattern := ".config/" + harness + "/creds.json"
	b, err := bundle.Pack(home, harness, "ws-test", []string{pattern}, []string{pattern}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPutGetRoundtrip(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	b := testBundle(t, "gh", "tok-v1")

	if _, _, _, err := s.Get(ctx, "alice", "gh"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get on empty store: want ErrNotFound, got %v", err)
	}
	rev, err := s.Put(ctx, "alice", "gh", b, "")
	if err != nil {
		t.Fatal(err)
	}
	if rev != "1" {
		t.Fatalf("first rev: want 1, got %s", rev)
	}
	got, gotRev, _, err := s.Get(ctx, "alice", "gh")
	if err != nil {
		t.Fatal(err)
	}
	if gotRev != "1" || got.Manifest.Harness != "gh" || len(got.Data) == 0 {
		t.Fatalf("roundtrip mismatch: rev=%s manifest=%+v", gotRev, got.Manifest)
	}
}

func TestPutCAS(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	b := testBundle(t, "gh", "tok-v1")

	if _, err := s.Put(ctx, "alice", "gh", b, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "alice", "gh", b, ""); !errors.Is(err, store.ErrCASConflict) {
		t.Fatalf("Put with empty expectRev on existing entry: want conflict, got %v", err)
	}
	if _, err := s.Put(ctx, "alice", "gh", b, "999"); !errors.Is(err, store.ErrCASConflict) {
		t.Fatalf("Put with stale expectRev: want conflict, got %v", err)
	}
	rev, err := s.Put(ctx, "alice", "gh", b, "1")
	if err != nil || rev != "2" {
		t.Fatalf("Put with matching rev: got rev=%s err=%v", rev, err)
	}
}

func TestLeaseLifecycle(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	ttl := 100 * time.Millisecond

	// Leases exist independently of bundles.
	l, err := s.Acquire(ctx, "alice", "claude", "wsA", ttl)
	if err != nil || l.Holder != "wsA" {
		t.Fatalf("acquire on empty entry: %+v %v", l, err)
	}
	if l2, err := s.Acquire(ctx, "alice", "claude", "wsB", ttl); !errors.Is(err, store.ErrLeaseHeld) {
		t.Fatalf("second acquire: want ErrLeaseHeld, got %+v %v", l2, err)
	} else if l2.Holder != "wsA" {
		t.Fatalf("ErrLeaseHeld should carry current holder, got %q", l2.Holder)
	}
	if _, err := s.Acquire(ctx, "alice", "claude", "wsA", ttl); err != nil {
		t.Fatalf("re-acquire by holder should renew: %v", err)
	}
	if _, err := s.Renew(ctx, "alice", "claude", "wsB", ttl); !errors.Is(err, store.ErrLeaseHeld) {
		t.Fatalf("renew by non-holder: want ErrLeaseHeld, got %v", err)
	}
	if err := s.Release(ctx, "alice", "claude", "wsB"); err != nil {
		t.Fatalf("release by non-holder must be a no-op: %v", err)
	}
	if err := s.Release(ctx, "alice", "claude", "wsA"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire(ctx, "alice", "claude", "wsB", ttl); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}

	// Expiry: a stopped workspace loses the lease on its own.
	time.Sleep(ttl + 20*time.Millisecond)
	if l, err := s.Acquire(ctx, "alice", "claude", "wsC", ttl); err != nil || l.Holder != "wsC" {
		t.Fatalf("takeover after expiry: %+v %v", l, err)
	}
}

func TestListAndDelete(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()

	if entries, err := s.List(ctx, "alice"); err != nil || len(entries) != 0 {
		t.Fatalf("list on empty store: %v %v", entries, err)
	}
	if _, err := s.Put(ctx, "alice", "gh", testBundle(t, "gh", "a"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "alice", "codex", testBundle(t, "codex", "b"), ""); err != nil {
		t.Fatal(err)
	}
	entries, err := s.List(ctx, "alice")
	if err != nil || len(entries) != 2 {
		t.Fatalf("list: want 2 entries, got %v %v", entries, err)
	}
	for _, e := range entries {
		if e.Instance != "ws-test" || e.Rev != "1" {
			t.Fatalf("entry metadata: %+v", e)
		}
	}
	if err := s.Delete(ctx, "alice", "gh"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Get(ctx, "alice", "gh"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get after delete: want ErrNotFound, got %v", err)
	}
	if entries, _ := s.List(ctx, "alice"); len(entries) != 1 {
		t.Fatalf("list after delete: want 1 entry, got %v", entries)
	}
}

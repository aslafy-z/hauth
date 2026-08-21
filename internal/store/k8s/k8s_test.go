package k8s

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

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

func TestSecretName(t *testing.T) {
	cases := []struct {
		user string
		want string
	}{
		{"alice", "harness-creds-alice"},
		{"Alice.Smith", "harness-creds-alice-smith-e6ac1b29"},
	}
	for _, c := range cases {
		got := SecretName(DefaultPrefix, c.user)
		if c.user == "alice" && got != c.want {
			t.Fatalf("SecretName(%q) = %q, want %q", c.user, got, c.want)
		}
		if c.user != "alice" {
			// Sanitized names must carry a hash suffix and stay DNS-1123.
			if !strings.HasPrefix(got, "harness-creds-alice-smith-") || len(got) > 253 {
				t.Fatalf("SecretName(%q) = %q", c.user, got)
			}
		}
	}
	// Distinct users that sanitize identically must not collide.
	a := SecretName(DefaultPrefix, "bob@example.com")
	b := SecretName(DefaultPrefix, "bob_example.com")
	if a == b {
		t.Fatalf("collision: %q", a)
	}
}

func TestPutGetCAS(t *testing.T) {
	s := New(fake.NewClientset(), "ws-ns", "")
	ctx := context.Background()
	b := testBundle(t, "codex", "tok-v1")

	if _, _, _, err := s.Get(ctx, "alice", "codex"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get before put: want ErrNotFound, got %v", err)
	}
	rev, err := s.Put(ctx, "alice", "codex", b, "")
	if err != nil || rev != "1" {
		t.Fatalf("first put: rev=%s err=%v", rev, err)
	}
	if _, err := s.Put(ctx, "alice", "codex", b, ""); !errors.Is(err, store.ErrCASConflict) {
		t.Fatalf("put with empty expectRev on existing: want conflict, got %v", err)
	}
	if _, err := s.Put(ctx, "alice", "codex", b, "42"); !errors.Is(err, store.ErrCASConflict) {
		t.Fatalf("put with stale rev: want conflict, got %v", err)
	}
	rev, err = s.Put(ctx, "alice", "codex", b, "1")
	if err != nil || rev != "2" {
		t.Fatalf("put with matching rev: rev=%s err=%v", rev, err)
	}
	got, gotRev, _, err := s.Get(ctx, "alice", "codex")
	if err != nil || gotRev != "2" || got.Manifest.Harness != "codex" {
		t.Fatalf("get: rev=%s manifest=%+v err=%v", gotRev, got.Manifest, err)
	}

	// A second harness lives in the same Secret without disturbing the first.
	if _, err := s.Put(ctx, "alice", "gh", testBundle(t, "gh", "x"), ""); err != nil {
		t.Fatal(err)
	}
	entries, err := s.List(ctx, "alice")
	if err != nil || len(entries) != 2 {
		t.Fatalf("list: %v %v", entries, err)
	}
}

func TestLeaseTakeoverAfterExpiry(t *testing.T) {
	s := New(fake.NewClientset(), "ws-ns", "")
	ctx := context.Background()
	ttl := 50 * time.Millisecond

	// Lease before any bundle exists (login acquires first).
	l, err := s.Acquire(ctx, "alice", "claude", "wsA", ttl)
	if err != nil || l.Holder != "wsA" {
		t.Fatalf("acquire: %+v %v", l, err)
	}
	if l2, err := s.Acquire(ctx, "alice", "claude", "wsB", ttl); !errors.Is(err, store.ErrLeaseHeld) || l2.Holder != "wsA" {
		t.Fatalf("acquire while held: %+v %v", l2, err)
	}
	time.Sleep(ttl + 20*time.Millisecond)
	if l, err := s.Acquire(ctx, "alice", "claude", "wsB", ttl); err != nil || l.Holder != "wsB" {
		t.Fatalf("takeover after expiry: %+v %v", l, err)
	}
	if _, err := s.Renew(ctx, "alice", "claude", "wsA", ttl); !errors.Is(err, store.ErrLeaseHeld) {
		t.Fatalf("renew after losing lease: want ErrLeaseHeld, got %v", err)
	}
	if err := s.Release(ctx, "alice", "claude", "wsA"); err != nil {
		t.Fatalf("release by non-holder must be a no-op: %v", err)
	}
	if _, err := s.Renew(ctx, "alice", "claude", "wsB", ttl); err != nil {
		t.Fatalf("renew by holder: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := New(fake.NewClientset(), "ws-ns", "")
	ctx := context.Background()

	if err := s.Delete(ctx, "alice", "gh"); err != nil {
		t.Fatalf("delete on missing secret: %v", err)
	}
	if _, err := s.Put(ctx, "alice", "gh", testBundle(t, "gh", "a"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "alice", "codex", testBundle(t, "codex", "b"), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "alice", "gh"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Get(ctx, "alice", "gh"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get after delete: want ErrNotFound, got %v", err)
	}
	entries, err := s.List(ctx, "alice")
	if err != nil || len(entries) != 1 || entries[0].Harness != "codex" {
		t.Fatalf("list after delete: %v %v", entries, err)
	}
}

package lease_test

import (
	"context"
	"testing"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/lease"
	"github.com/aslafy-z/hauth/internal/store/file"
)

func TestEnsureHolderAndShouldPush(t *testing.T) {
	st := file.New(t.TempDir())
	ctx := context.Background()
	a := lease.NewManager(st, "alice", "wsA", time.Second)
	b := lease.NewManager(st, "alice", "wsB", time.Second)

	held, holder, err := a.EnsureHolder(ctx, "claude")
	if err != nil || !held || holder != "wsA" {
		t.Fatalf("A EnsureHolder: %v %q %v", held, holder, err)
	}
	held, holder, err = b.EnsureHolder(ctx, "claude")
	if err != nil || held || holder != "wsA" {
		t.Fatalf("B EnsureHolder while A holds: %v %q %v", held, holder, err)
	}

	if ok, _ := b.ShouldPush(ctx, "claude", adapter.RefreshRotating); ok {
		t.Fatal("rotating: non-holder must not push")
	}
	if ok, _ := b.ShouldPush(ctx, "claude", adapter.RefreshStable); !ok {
		t.Fatal("stable: everyone may push")
	}
	if ok, _ := b.ShouldPush(ctx, "claude", adapter.RefreshNone); ok {
		t.Fatal("none: nothing to push")
	}
}

func TestRunRenewalReleasesOnCancel(t *testing.T) {
	st := file.New(t.TempDir())
	ctx := context.Background()
	ttl := 300 * time.Millisecond
	a := lease.NewManager(st, "alice", "wsA", ttl)
	b := lease.NewManager(st, "alice", "wsB", ttl)

	if held, _, _ := a.EnsureHolder(ctx, "codex"); !held {
		t.Fatal("A should hold")
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		a.RunRenewal(rctx, "codex")
		close(done)
	}()
	// The renewal loop keeps the lease alive past its own TTL.
	time.Sleep(ttl + ttl/2)
	if held, holder, _ := b.EnsureHolder(ctx, "codex"); held {
		t.Fatalf("B acquired while A renews (holder %q)", holder)
	}
	cancel()
	<-done
	// Release on shutdown means B does not have to wait for expiry.
	if held, _, _ := b.EnsureHolder(ctx, "codex"); !held {
		t.Fatal("B could not acquire after A released")
	}
}

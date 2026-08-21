package syncer_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/source"
	"github.com/aslafy-z/hauth/internal/store"
	"github.com/aslafy-z/hauth/internal/store/file"
	"github.com/aslafy-z/hauth/internal/syncer"
)

var adapterSeq atomic.Int64

// fakeAdapter is a minimal adapter with one credential file, registered
// under a unique name so tests do not collide in the global registry.
type fakeAdapter struct {
	name   string
	policy adapter.RefreshPolicy
}

func newFakeAdapter(policy adapter.RefreshPolicy) *fakeAdapter {
	a := &fakeAdapter{
		name:   fmt.Sprintf("sim%s%d", policy, adapterSeq.Add(1)),
		policy: policy,
	}
	adapter.Register(a)
	return a
}

func (a *fakeAdapter) Name() string                   { return a.name }
func (a *fakeAdapter) Paths() []string                { return []string{".config/" + a.name + "/creds.json"} }
func (a *fakeAdapter) IdentityPaths() []string        { return a.Paths() }
func (a *fakeAdapter) EnvAuth() []adapter.EnvAuthSpec { return nil }
func (a *fakeAdapter) EnvOverridesFiles() bool        { return false }
func (a *fakeAdapter) Methods() []adapter.Method      { return nil }
func (a *fakeAdapter) Refresh() adapter.RefreshPolicy { return a.policy }
func (a *fakeAdapter) Probe(ctx context.Context, cheap bool) (adapter.Status, error) {
	return adapter.Status{}, nil
}
func (a *fakeAdapter) Logout(ctx context.Context, src adapter.Source) error { return nil }

// workspace bundles a Syncer with its private home and state dirs, standing
// in for one Coder workspace.
type workspace struct {
	name  string
	home  string
	state string
	sync  *syncer.Syncer
}

func newWorkspace(t *testing.T, st store.Store, name string, ttl time.Duration) *workspace {
	t.Helper()
	ws := &workspace{name: name, home: t.TempDir(), state: t.TempDir()}
	ws.sync = syncer.New(syncer.Config{
		Store:    st,
		User:     "alice",
		Instance: name,
		Home:     ws.home,
		StateDir: ws.state,
		TTL:      ttl,
		Debounce: 10 * time.Millisecond,
	})
	return ws
}

func (ws *workspace) credPath(a *fakeAdapter) string {
	return filepath.Join(ws.home, filepath.FromSlash(a.Paths()[0]))
}

func (ws *workspace) writeCreds(t *testing.T, a *fakeAdapter, content string) {
	t.Helper()
	p := ws.credPath(a)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (ws *workspace) readCreds(t *testing.T, a *fakeAdapter) string {
	t.Helper()
	data, err := os.ReadFile(ws.credPath(a))
	if err != nil {
		return ""
	}
	return string(data)
}

func TestRotatingTwoWorkspaces(t *testing.T) {
	fa := newFakeAdapter(adapter.RefreshRotating)
	st := file.New(t.TempDir())
	ctx := context.Background()
	ttl := 200 * time.Millisecond

	wsA := newWorkspace(t, st, "wsA", ttl)
	wsB := newWorkspace(t, st, "wsB", ttl)

	// Login happens on A: hauth seeds the store (force, like the login flow).
	wsA.writeCreds(t, fa, "token-v1")
	if pushed, err := wsA.sync.PushHarness(ctx, fa.Name(), true); err != nil || !pushed {
		t.Fatalf("seed push: pushed=%v err=%v", pushed, err)
	}

	// B starts and restores.
	if err := wsB.sync.RestoreAll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := wsB.readCreds(t, fa); got != "token-v1" {
		t.Fatalf("B after restore: %q", got)
	}

	// The CLI on A rotates the token; A pushes (acquiring the lease).
	wsA.writeCreds(t, fa, "token-v2")
	if pushed, err := wsA.sync.PushHarness(ctx, fa.Name(), false); err != nil || !pushed {
		t.Fatalf("rotation push on A: pushed=%v err=%v", pushed, err)
	}

	// B's local copy also rotates, but A holds the lease: B must not push.
	wsB.writeCreds(t, fa, "token-v2b")
	if pushed, err := wsB.sync.PushHarness(ctx, fa.Name(), false); err != nil || pushed {
		t.Fatalf("push on B while A holds lease: pushed=%v err=%v", pushed, err)
	}
	if _, rev, _, _ := st.Get(ctx, "alice", fa.Name()); rev != "2" {
		t.Fatalf("store rev after blocked push: want 2, got %s", rev)
	}

	// A stops renewing; after TTL expiry B takes over and pushes.
	time.Sleep(ttl + 50*time.Millisecond)
	if pushed, err := wsB.sync.PushHarness(ctx, fa.Name(), false); err != nil || !pushed {
		t.Fatalf("takeover push on B: pushed=%v err=%v", pushed, err)
	}

	// A restarts: its record matches its own v2, so the store's v2b wins.
	if err := wsA.sync.RestoreAll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := wsA.readCreds(t, fa); got != "token-v2b" {
		t.Fatalf("A after takeover restore: %q", got)
	}
}

func TestStableLastWriterWins(t *testing.T) {
	fa := newFakeAdapter(adapter.RefreshStable)
	st := file.New(t.TempDir())
	ctx := context.Background()

	wsA := newWorkspace(t, st, "wsA", 0)
	wsB := newWorkspace(t, st, "wsB", 0)

	wsA.writeCreds(t, fa, "hosts-v1")
	if pushed, err := wsA.sync.PushHarness(ctx, fa.Name(), true); err != nil || !pushed {
		t.Fatalf("seed: %v %v", pushed, err)
	}
	if err := wsB.sync.RestoreAll(ctx); err != nil {
		t.Fatal(err)
	}

	// Both sides may push under Stable; no lease involved.
	wsB.writeCreds(t, fa, "hosts-v2")
	if pushed, err := wsB.sync.PushHarness(ctx, fa.Name(), false); err != nil || !pushed {
		t.Fatalf("B push: %v %v", pushed, err)
	}
	wsA.writeCreds(t, fa, "hosts-v3")
	if pushed, err := wsA.sync.PushHarness(ctx, fa.Name(), false); err != nil || !pushed {
		t.Fatalf("A push after B: %v %v", pushed, err)
	}

	// Unchanged content is skipped by the content hash, not re-pushed.
	if pushed, err := wsA.sync.PushHarness(ctx, fa.Name(), false); err != nil || pushed {
		t.Fatalf("no-op push: pushed=%v err=%v", pushed, err)
	}
	if _, rev, _, _ := st.Get(ctx, "alice", fa.Name()); rev != "3" {
		t.Fatalf("rev after pushes: want 3, got %s", rev)
	}
}

func TestFileExternalNeverClobberedOrAdopted(t *testing.T) {
	fa := newFakeAdapter(adapter.RefreshRotating)
	st := file.New(t.TempDir())
	ctx := context.Background()

	wsA := newWorkspace(t, st, "wsA", 0)
	wsB := newWorkspace(t, st, "wsB", 0)

	wsA.writeCreds(t, fa, "store-token")
	if _, err := wsA.sync.PushHarness(ctx, fa.Name(), true); err != nil {
		t.Fatal(err)
	}

	// B already has credentials hauth never wrote (mounted secret, manual
	// login). Restore must not overwrite them.
	wsB.writeCreds(t, fa, "personal-token")
	if err := wsB.sync.RestoreAll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := wsB.readCreds(t, fa); got != "personal-token" {
		t.Fatalf("file-external creds clobbered: %q", got)
	}

	// And the watcher path must not adopt them into the store.
	if pushed, err := wsB.sync.PushHarness(ctx, fa.Name(), false); err != nil || pushed {
		t.Fatalf("implicit adoption: pushed=%v err=%v", pushed, err)
	}

	// Explicit adoption is the only way in.
	if err := wsB.sync.Adopt(ctx, fa.Name()); err != nil {
		t.Fatal(err)
	}
	got, _, _, err := st.Get(ctx, "alice", fa.Name())
	if err != nil {
		t.Fatal(err)
	}
	if src, _ := source.Resolve(fa, wsB.home, wsB.state); src != adapter.SourceStore {
		t.Fatalf("after adopt: want SourceStore, got %v", src)
	}
	if len(got.Manifest.Files) != 1 {
		t.Fatalf("adopted manifest: %+v", got.Manifest)
	}
}

func TestDeleteHarness(t *testing.T) {
	fa := newFakeAdapter(adapter.RefreshRotating)
	st := file.New(t.TempDir())
	ctx := context.Background()

	ws := newWorkspace(t, st, "wsA", 0)
	ws.writeCreds(t, fa, "tok")
	if _, err := ws.sync.PushHarness(ctx, fa.Name(), true); err != nil {
		t.Fatal(err)
	}
	if err := ws.sync.DeleteHarness(ctx, fa.Name()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.Get(ctx, "alice", fa.Name()); err == nil {
		t.Fatal("store entry still present after delete")
	}
	// Local files remain (logout handles those), but they now resolve as
	// file-external because the restored record is gone.
	if src, _ := source.Resolve(fa, ws.home, ws.state); src != adapter.SourceFileExternal {
		t.Fatalf("after delete: want SourceFileExternal, got %v", src)
	}
}

func TestWatchPushesOnChange(t *testing.T) {
	fa := newFakeAdapter(adapter.RefreshStable)
	st := file.New(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ws := newWorkspace(t, st, "wsA", 0)
	ws.writeCreds(t, fa, "v1")
	if _, err := ws.sync.PushHarness(ctx, fa.Name(), true); err != nil {
		t.Fatal(err)
	}

	changed := make(chan string, 16)
	go func() {
		_ = ws.sync.Watch(ctx, func(h string) { changed <- h })
	}()
	time.Sleep(100 * time.Millisecond) // let the watcher install its watches

	ws.writeCreds(t, fa, "v2")
	deadline := time.After(3 * time.Second)
	for {
		select {
		case h := <-changed:
			if h == fa.Name() {
				return
			}
		case <-deadline:
			t.Fatal("watcher never reported the change")
		}
	}
}

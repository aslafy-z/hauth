package app

import (
	"context"
	"testing"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/adapter/fake"
)

// Check must serve a fresh expensive result from cache unless forced, so
// page loads can fan out checks without re-running probes that cost a
// network or model call.
func TestCheckServesFreshExpensiveFromCache(t *testing.T) {
	cfg := common.Config{Home: t.TempDir(), StateDir: t.TempDir()}
	if _, ok := adapter.Get("fake"); !ok {
		adapter.Register(fake.New(cfg, "/nonexistent/fakecli"))
	}
	a := New(Config{Home: cfg.Home, StateDir: cfg.StateDir})
	a.status["fake"] = cachedStatus{
		st:        adapter.Status{Source: adapter.SourceStore, Valid: adapter.ValidityValid, CheckedAt: time.Now()},
		at:        time.Now(),
		expensive: true,
	}

	v, err := a.Check(context.Background(), "fake", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.Validity != adapter.ValidityValid.String() || v.Source != adapter.SourceStore.String() {
		t.Fatalf("cached result not served: %+v", v)
	}

	// force bypasses the cache: with an empty home the probe finds nothing.
	v, err = a.Check(context.Background(), "fake", true)
	if err != nil {
		t.Fatal(err)
	}
	if v.Source != adapter.SourceNone.String() {
		t.Fatalf("force did not re-probe: %+v", v)
	}
}

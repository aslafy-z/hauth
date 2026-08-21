// Package lease applies the refresh-policy rules from the design: Rotating
// harnesses are refreshed by exactly one instance (the lease holder), Stable
// harnesses are last-writer-wins, None harnesses never sync.
package lease

import (
	"context"
	"errors"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/store"
)

// DefaultTTL keeps leases short so a stopped workspace loses its lease on
// its own; the holder's renewal loop runs at TTL/3.
const DefaultTTL = 10 * time.Minute

// Manager decides lease questions for one (store, user, instance) triple.
type Manager struct {
	st       store.Store
	user     string
	instance string
	ttl      time.Duration
}

// NewManager builds a Manager. ttl <= 0 means DefaultTTL.
func NewManager(st store.Store, user, instance string, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Manager{st: st, user: user, instance: instance, ttl: ttl}
}

// TTL returns the manager's lease duration.
func (m *Manager) TTL() time.Duration { return m.ttl }

// EnsureHolder tries to hold the lease for harness, acquiring or renewing.
// held=false with a nil error means another live instance holds it; holder
// then names that instance.
func (m *Manager) EnsureHolder(ctx context.Context, harness string) (held bool, holder string, err error) {
	l, err := m.st.Acquire(ctx, m.user, harness, m.instance, m.ttl)
	if errors.Is(err, store.ErrLeaseHeld) {
		return false, l.Holder, nil
	}
	if err != nil {
		return false, "", err
	}
	return true, m.instance, nil
}

// ShouldPush reports whether this instance may push harness under policy.
func (m *Manager) ShouldPush(ctx context.Context, harness string, policy adapter.RefreshPolicy) (bool, error) {
	switch policy {
	case adapter.RefreshNone:
		return false, nil
	case adapter.RefreshStable:
		return true, nil
	default:
		held, _, err := m.EnsureHolder(ctx, harness)
		return held, err
	}
}

// RunRenewal renews the lease at TTL/3 until ctx is canceled, then releases
// it. It returns early if the lease moves to another instance.
func (m *Manager) RunRenewal(ctx context.Context, harness string) {
	t := time.NewTicker(m.ttl / 3)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = m.st.Release(rctx, m.user, harness, m.instance)
			cancel()
			return
		case <-t.C:
			if _, err := m.st.Renew(ctx, m.user, harness, m.instance, m.ttl); err != nil {
				return
			}
		}
	}
}

// Release drops the lease if this instance holds it.
func (m *Manager) Release(ctx context.Context, harness string) error {
	return m.st.Release(ctx, m.user, harness, m.instance)
}

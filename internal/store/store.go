// Package store defines the per-user credential store: a key-value of
// {harness -> bundle} plus a lease record per harness. Backends implement
// compare-and-swap on a revision so concurrent workspaces cannot silently
// clobber each other.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/aslafy-z/hauth/internal/bundle"
)

var (
	// ErrNotFound: no bundle stored for this user/harness.
	ErrNotFound = errors.New("store: entry not found")
	// ErrCASConflict: expectRev did not match the current revision.
	ErrCASConflict = errors.New("store: revision conflict")
	// ErrLeaseHeld: another instance holds a live lease.
	ErrLeaseHeld = errors.New("store: lease held by another instance")
)

// Lease records which instance may refresh a harness right now.
type Lease struct {
	Holder    string    `json:"holder"` // HAUTH_INSTANCE of the holder
	ExpiresAt time.Time `json:"expires_at"`
}

// Live reports whether the lease is held at time now.
func (l Lease) Live(now time.Time) bool {
	return l.Holder != "" && now.Before(l.ExpiresAt)
}

// Entry summarizes one stored harness for listings.
type Entry struct {
	Harness   string    `json:"harness"`
	Rev       string    `json:"rev"`
	UpdatedAt time.Time `json:"updated_at"`
	Instance  string    `json:"instance"` // instance that wrote the bundle
	Lease     Lease     `json:"lease"`
}

// Store is implemented by each backend (file, kubernetes).
//
// Revisions are opaque strings. Put with expectRev "" requires that no entry
// exists yet; otherwise expectRev must equal the current revision or
// ErrCASConflict is returned.
type Store interface {
	// Get returns the stored bundle, its revision, and the current lease.
	Get(ctx context.Context, user, harness string) (bundle.Bundle, string, Lease, error)

	// Put stores a bundle under CAS semantics and returns the new revision.
	Put(ctx context.Context, user, harness string, b bundle.Bundle, expectRev string) (string, error)

	// Acquire takes the lease for instance, or returns ErrLeaseHeld if a
	// different instance holds a live lease. Acquiring a lease you already
	// hold renews it.
	Acquire(ctx context.Context, user, harness, instance string, ttl time.Duration) (Lease, error)

	// Renew extends a lease held by instance. Returns ErrLeaseHeld if the
	// lease moved to someone else.
	Renew(ctx context.Context, user, harness, instance string, ttl time.Duration) (Lease, error)

	// Release drops the lease if held by instance. Releasing a lease you
	// do not hold is a no-op.
	Release(ctx context.Context, user, harness, instance string) error

	// List returns entries for all harnesses stored for user.
	List(ctx context.Context, user string) ([]Entry, error)

	// Delete removes the bundle and lease for user/harness.
	Delete(ctx context.Context, user, harness string) error
}

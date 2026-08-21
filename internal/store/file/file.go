// Package file implements the store on a local directory. It exists for
// tests and for non-Coder use (a shared volume, a synced directory). Layout:
//
//	<root>/<user>/<harness>/{bundle.tar.gz, manifest.json, lease.json, rev}
//
// The revision is a monotonically increasing integer serialized as a string.
// A per-harness flock serializes read-modify-write cycles between processes;
// CAS compares the rev file content against the caller's expectation.
package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aslafy-z/hauth/internal/bundle"
	"github.com/aslafy-z/hauth/internal/store"
)

const (
	bundleFile   = "bundle.tar.gz"
	manifestFile = "manifest.json"
	leaseFile    = "lease.json"
	revFile      = "rev"
	lockFile     = ".lock"
)

// Store is a file-backed store.Store.
type Store struct {
	root string
}

// New returns a store rooted at dir. The directory is created lazily.
func New(root string) *Store {
	return &Store{root: root}
}

// sanitize keeps user and harness names safe as single path components.
func sanitize(name string) string {
	if name == "" || name == "." || name == ".." {
		return "_"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func (s *Store) harnessDir(user, harness string) string {
	return filepath.Join(s.root, sanitize(user), sanitize(harness))
}

// withLock runs fn while holding an exclusive lock on the harness directory.
func (s *Store) withLock(dir string, fn func() error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := flock(f); err != nil {
		return err
	}
	defer funlock(f)
	return fn()
}

func readRev(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, revFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readLease(dir string) store.Lease {
	var l store.Lease
	data, err := os.ReadFile(filepath.Join(dir, leaseFile))
	if err != nil {
		return l
	}
	_ = json.Unmarshal(data, &l)
	return l
}

func writeFileAtomic(abs string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, abs)
}

func writeLease(dir string, l store.Lease) error {
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, leaseFile), data)
}

func (s *Store) Get(ctx context.Context, user, harness string) (bundle.Bundle, string, store.Lease, error) {
	dir := s.harnessDir(user, harness)
	var (
		b   bundle.Bundle
		rev string
		l   store.Lease
	)
	err := s.withLock(dir, func() error {
		rev = readRev(dir)
		if rev == "" {
			return store.ErrNotFound
		}
		manData, err := os.ReadFile(filepath.Join(dir, manifestFile))
		if err != nil {
			return fmt.Errorf("read manifest: %w", err)
		}
		if err := json.Unmarshal(manData, &b.Manifest); err != nil {
			return fmt.Errorf("decode manifest: %w", err)
		}
		b.Data, err = os.ReadFile(filepath.Join(dir, bundleFile))
		if err != nil {
			return fmt.Errorf("read bundle: %w", err)
		}
		l = readLease(dir)
		return nil
	})
	if err != nil {
		return bundle.Bundle{}, "", store.Lease{}, err
	}
	return b, rev, l, nil
}

func (s *Store) Put(ctx context.Context, user, harness string, b bundle.Bundle, expectRev string) (string, error) {
	dir := s.harnessDir(user, harness)
	var newRev string
	err := s.withLock(dir, func() error {
		cur := readRev(dir)
		if cur != expectRev {
			return store.ErrCASConflict
		}
		n := 0
		if cur != "" {
			var err error
			n, err = strconv.Atoi(cur)
			if err != nil {
				return fmt.Errorf("corrupt revision %q: %w", cur, err)
			}
		}
		newRev = strconv.Itoa(n + 1)
		manData, err := json.MarshalIndent(b.Manifest, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(dir, bundleFile), b.Data); err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(dir, manifestFile), manData); err != nil {
			return err
		}
		// rev last: an interrupted write never yields a rev pointing at
		// missing or stale content newer than itself.
		return writeFileAtomic(filepath.Join(dir, revFile), []byte(newRev))
	})
	if err != nil {
		return "", err
	}
	return newRev, nil
}

func (s *Store) Acquire(ctx context.Context, user, harness, instance string, ttl time.Duration) (store.Lease, error) {
	dir := s.harnessDir(user, harness)
	var out store.Lease
	err := s.withLock(dir, func() error {
		cur := readLease(dir)
		now := time.Now()
		if cur.Live(now) && cur.Holder != instance {
			out = cur
			return store.ErrLeaseHeld
		}
		out = store.Lease{Holder: instance, ExpiresAt: now.Add(ttl)}
		return writeLease(dir, out)
	})
	return out, err
}

func (s *Store) Renew(ctx context.Context, user, harness, instance string, ttl time.Duration) (store.Lease, error) {
	dir := s.harnessDir(user, harness)
	var out store.Lease
	err := s.withLock(dir, func() error {
		cur := readLease(dir)
		if cur.Holder != instance {
			out = cur
			return store.ErrLeaseHeld
		}
		out = store.Lease{Holder: instance, ExpiresAt: time.Now().Add(ttl)}
		return writeLease(dir, out)
	})
	return out, err
}

func (s *Store) Release(ctx context.Context, user, harness, instance string) error {
	dir := s.harnessDir(user, harness)
	return s.withLock(dir, func() error {
		cur := readLease(dir)
		if cur.Holder != instance {
			return nil
		}
		err := os.Remove(filepath.Join(dir, leaseFile))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	})
}

func (s *Store) List(ctx context.Context, user string) ([]store.Entry, error) {
	userDir := filepath.Join(s.root, sanitize(user))
	dirents, err := os.ReadDir(userDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []store.Entry
	for _, d := range dirents {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(userDir, d.Name())
		var e store.Entry
		err := s.withLock(dir, func() error {
			rev := readRev(dir)
			if rev == "" {
				return store.ErrNotFound
			}
			var m bundle.Manifest
			manData, err := os.ReadFile(filepath.Join(dir, manifestFile))
			if err == nil {
				_ = json.Unmarshal(manData, &m)
			}
			harness := m.Harness
			if harness == "" {
				harness = d.Name()
			}
			e = store.Entry{
				Harness:   harness,
				Rev:       rev,
				UpdatedAt: m.CreatedAt,
				Instance:  m.Instance,
				Lease:     readLease(dir),
			}
			return nil
		})
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (s *Store) Delete(ctx context.Context, user, harness string) error {
	dir := s.harnessDir(user, harness)
	return s.withLock(dir, func() error {
		return os.RemoveAll(dir)
	})
}

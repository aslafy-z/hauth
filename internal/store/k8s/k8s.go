// Package k8s implements the store on one Kubernetes Secret per user in a
// single namespace. Data keys are namespaced by harness:
//
//	<harness>.tar.gz, <harness>.manifest.json, <harness>.lease.json, <harness>.rev
//
// Optimistic concurrency uses the Secret's resourceVersion; hauth's own CAS
// compares the per-harness rev key. RBAC scoping to "Secrets matching the
// owner" is the deployment's job (see deploy/k8s).
package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/aslafy-z/hauth/internal/bundle"
	"github.com/aslafy-z/hauth/internal/store"
)

// DefaultPrefix is the default Secret name prefix.
const DefaultPrefix = "harness-creds-"

// casRetries bounds retry loops on resourceVersion conflicts. The rev check
// still decides correctness; retries only absorb unrelated concurrent writes
// to the same Secret (other harnesses, lease renewals).
const casRetries = 5

// Store is a Kubernetes-Secret-backed store.Store.
type Store struct {
	client    kubernetes.Interface
	namespace string
	prefix    string
}

// New returns a store using client in namespace. An empty prefix means
// DefaultPrefix.
func New(client kubernetes.Interface, namespace, prefix string) *Store {
	if prefix == "" {
		prefix = DefaultPrefix
	}
	return &Store{client: client, namespace: namespace, prefix: prefix}
}

// NewInCluster builds a store from in-cluster credentials, falling back to
// the default kubeconfig so `hauth store` works from outside the cluster.
func NewInCluster(namespace, prefix string) (*Store, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("no in-cluster config and no kubeconfig: %w", err)
		}
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return New(client, namespace, prefix), nil
}

// SecretName returns the Secret name used for user. Names are sanitized into
// DNS-1123; a stable hash suffix keeps distinct users distinct whenever
// sanitization altered or truncated the name.
func SecretName(prefix, user string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(user) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	clean := strings.Trim(b.String(), "-")
	name := prefix + clean
	const maxLen = 253
	if clean == strings.ToLower(user) && clean != "" && len(name) <= maxLen {
		return name
	}
	sum := sha256.Sum256([]byte(user))
	suffix := hex.EncodeToString(sum[:])[:8]
	if max := maxLen - len(suffix) - 1; len(name) > max {
		name = name[:max]
	}
	return strings.Trim(name, "-") + "-" + suffix
}

func (s *Store) secretName(user string) string {
	return SecretName(s.prefix, user)
}

func keyBundle(h string) string   { return h + ".tar.gz" }
func keyManifest(h string) string { return h + ".manifest.json" }
func keyLease(h string) string    { return h + ".lease.json" }
func keyRev(h string) string      { return h + ".rev" }

func (s *Store) get(ctx context.Context, user string) (*corev1.Secret, error) {
	return s.client.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName(user), metav1.GetOptions{})
}

func newSecret(name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "hauth",
			},
		},
		Data: map[string][]byte{},
	}
}

func leaseFrom(sec *corev1.Secret, harness string) store.Lease {
	var l store.Lease
	if data, ok := sec.Data[keyLease(harness)]; ok {
		_ = json.Unmarshal(data, &l)
	}
	return l
}

func (s *Store) Get(ctx context.Context, user, harness string) (bundle.Bundle, string, store.Lease, error) {
	sec, err := s.get(ctx, user)
	if apierrors.IsNotFound(err) {
		return bundle.Bundle{}, "", store.Lease{}, store.ErrNotFound
	}
	if err != nil {
		return bundle.Bundle{}, "", store.Lease{}, err
	}
	rev := string(sec.Data[keyRev(harness)])
	if rev == "" {
		return bundle.Bundle{}, "", store.Lease{}, store.ErrNotFound
	}
	var b bundle.Bundle
	if err := json.Unmarshal(sec.Data[keyManifest(harness)], &b.Manifest); err != nil {
		return bundle.Bundle{}, "", store.Lease{}, fmt.Errorf("decode manifest: %w", err)
	}
	b.Data = sec.Data[keyBundle(harness)]
	return b, rev, leaseFrom(sec, harness), nil
}

func (s *Store) Put(ctx context.Context, user, harness string, b bundle.Bundle, expectRev string) (string, error) {
	manData, err := json.Marshal(b.Manifest)
	if err != nil {
		return "", err
	}
	name := s.secretName(user)
	for i := 0; i < casRetries; i++ {
		sec, err := s.get(ctx, user)
		if apierrors.IsNotFound(err) {
			if expectRev != "" {
				return "", store.ErrCASConflict
			}
			sec = newSecret(name)
			sec.Data[keyBundle(harness)] = b.Data
			sec.Data[keyManifest(harness)] = manData
			sec.Data[keyRev(harness)] = []byte("1")
			if _, err := s.client.CoreV1().Secrets(s.namespace).Create(ctx, sec, metav1.CreateOptions{}); err != nil {
				if apierrors.IsAlreadyExists(err) {
					continue
				}
				return "", err
			}
			return "1", nil
		}
		if err != nil {
			return "", err
		}
		cur := string(sec.Data[keyRev(harness)])
		if cur != expectRev {
			return "", store.ErrCASConflict
		}
		n := 0
		if cur != "" {
			n, err = strconv.Atoi(cur)
			if err != nil {
				return "", fmt.Errorf("corrupt revision %q: %w", cur, err)
			}
		}
		newRev := strconv.Itoa(n + 1)
		if sec.Data == nil {
			sec.Data = map[string][]byte{}
		}
		sec.Data[keyBundle(harness)] = b.Data
		sec.Data[keyManifest(harness)] = manData
		sec.Data[keyRev(harness)] = []byte(newRev)
		if _, err := s.client.CoreV1().Secrets(s.namespace).Update(ctx, sec, metav1.UpdateOptions{}); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return "", err
		}
		return newRev, nil
	}
	return "", store.ErrCASConflict
}

// mutateLease applies fn to the current lease under retry. fn returns the
// new lease content (nil to delete the key) or an error to abort.
func (s *Store) mutateLease(ctx context.Context, user, harness string, fn func(cur store.Lease) (*store.Lease, error)) (store.Lease, error) {
	name := s.secretName(user)
	for i := 0; i < casRetries; i++ {
		sec, err := s.get(ctx, user)
		created := false
		if apierrors.IsNotFound(err) {
			sec = newSecret(name)
			created = true
		} else if err != nil {
			return store.Lease{}, err
		}
		next, err := fn(leaseFrom(sec, harness))
		if err != nil {
			return store.Lease{}, err
		}
		if next == nil {
			if created {
				return store.Lease{}, nil
			}
			delete(sec.Data, keyLease(harness))
		} else {
			data, err := json.Marshal(next)
			if err != nil {
				return store.Lease{}, err
			}
			if sec.Data == nil {
				sec.Data = map[string][]byte{}
			}
			sec.Data[keyLease(harness)] = data
		}
		if created {
			_, err = s.client.CoreV1().Secrets(s.namespace).Create(ctx, sec, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				continue
			}
		} else {
			_, err = s.client.CoreV1().Secrets(s.namespace).Update(ctx, sec, metav1.UpdateOptions{})
			if apierrors.IsConflict(err) {
				continue
			}
		}
		if err != nil {
			return store.Lease{}, err
		}
		if next == nil {
			return store.Lease{}, nil
		}
		return *next, nil
	}
	return store.Lease{}, store.ErrCASConflict
}

func (s *Store) Acquire(ctx context.Context, user, harness, instance string, ttl time.Duration) (store.Lease, error) {
	var held store.Lease
	l, err := s.mutateLease(ctx, user, harness, func(cur store.Lease) (*store.Lease, error) {
		now := time.Now()
		if cur.Live(now) && cur.Holder != instance {
			held = cur
			return nil, store.ErrLeaseHeld
		}
		return &store.Lease{Holder: instance, ExpiresAt: now.Add(ttl)}, nil
	})
	if err == store.ErrLeaseHeld {
		return held, err
	}
	return l, err
}

func (s *Store) Renew(ctx context.Context, user, harness, instance string, ttl time.Duration) (store.Lease, error) {
	var held store.Lease
	l, err := s.mutateLease(ctx, user, harness, func(cur store.Lease) (*store.Lease, error) {
		if cur.Holder != instance {
			held = cur
			return nil, store.ErrLeaseHeld
		}
		return &store.Lease{Holder: instance, ExpiresAt: time.Now().Add(ttl)}, nil
	})
	if err == store.ErrLeaseHeld {
		return held, err
	}
	return l, err
}

func (s *Store) Release(ctx context.Context, user, harness, instance string) error {
	_, err := s.mutateLease(ctx, user, harness, func(cur store.Lease) (*store.Lease, error) {
		if cur.Holder != instance {
			return &cur, nil
		}
		return nil, nil
	})
	return err
}

func (s *Store) List(ctx context.Context, user string) ([]store.Entry, error) {
	sec, err := s.get(ctx, user)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []store.Entry
	for key, val := range sec.Data {
		harness, ok := strings.CutSuffix(key, ".rev")
		if !ok {
			continue
		}
		var m bundle.Manifest
		_ = json.Unmarshal(sec.Data[keyManifest(harness)], &m)
		entries = append(entries, store.Entry{
			Harness:   harness,
			Rev:       string(val),
			UpdatedAt: m.CreatedAt,
			Instance:  m.Instance,
			Lease:     leaseFrom(sec, harness),
		})
	}
	return entries, nil
}

func (s *Store) Delete(ctx context.Context, user, harness string) error {
	for i := 0; i < casRetries; i++ {
		sec, err := s.get(ctx, user)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		delete(sec.Data, keyBundle(harness))
		delete(sec.Data, keyManifest(harness))
		delete(sec.Data, keyLease(harness))
		delete(sec.Data, keyRev(harness))
		if _, err := s.client.CoreV1().Secrets(s.namespace).Update(ctx, sec, metav1.UpdateOptions{}); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return err
		}
		return nil
	}
	return store.ErrCASConflict
}

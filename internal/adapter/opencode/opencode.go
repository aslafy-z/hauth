// Package opencode adapts the OpenCode CLI. Credentials for all providers
// live in one auth.json; login is an interactive provider picker.
package opencode

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/source"
)

const authRel = ".local/share/opencode/auth.json"

type opencodeAdapter struct {
	cfg common.Config
}

// New returns the opencode adapter.
func New(cfg common.Config) adapter.Adapter {
	return &opencodeAdapter{cfg: cfg.Complete()}
}

func (o *opencodeAdapter) Name() string            { return "opencode" }
func (o *opencodeAdapter) Paths() []string         { return []string{authRel} }
func (o *opencodeAdapter) IdentityPaths() []string { return o.Paths() }

// No documented env short-circuit: provider keys are stored via auth login.
func (o *opencodeAdapter) EnvAuth() []adapter.EnvAuthSpec { return nil }
func (o *opencodeAdapter) EnvOverridesFiles() bool        { return false }

// Provider-dependent; some providers rotate OAuth tokens, so treat the
// whole file as rotating.
func (o *opencodeAdapter) Refresh() adapter.RefreshPolicy { return adapter.RefreshRotating }

func (o *opencodeAdapter) Methods() []adapter.Method {
	return []adapter.Method{
		{
			ID:    "paste",
			Title: "Interactive provider login",
			Argv:  []string{"opencode", "auth", "login"},
			// The picker UI varies per provider; explicit rules cover the
			// ChatGPT device flow ("Go to: <url>" / "Enter code: <code>"),
			// and the generic URL fallback plus the terminal panel carry
			// the rest.
			Detect: func() adapter.Detector {
				return adapter.NewLineDetector(
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)go to[:\s]+(?P<url>https://\S+)`), Kind: adapter.EventURL, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)enter code[:\s]+(?P<code>[A-Z0-9]{3,10}-[A-Z0-9]{3,10})`), Kind: adapter.EventCode, Once: true},
				)
			},
			Input:   adapter.InputFreeText,
			Timeout: 10 * time.Minute,
		},
	}
}

func (o *opencodeAdapter) Probe(ctx context.Context, cheap bool) (adapter.Status, error) {
	src, detail := source.Resolve(o, o.cfg.Home, o.cfg.StateDir)
	st := adapter.Status{Source: src, Detail: detail, Valid: adapter.ValidityUnknown, CheckedAt: time.Now()}
	if src == adapter.SourceNone {
		return st, nil
	}
	o.readAuth(&st)
	if cheap {
		return st, nil
	}
	// auth list only proves the file parses; validity would need a
	// provider call, so it stays unknown unless the CLI itself errors.
	_, _, code, err := o.cfg.Run(ctx, "opencode", "auth", "list")
	if err == nil && code != 0 {
		st.Valid = adapter.ValidityInvalid
	}
	return st, nil
}

// readAuth surfaces the per-provider entries of auth.json: which providers
// are logged in (as the account) and, for OAuth entries, validity from the
// access-token expiry (ms epoch). An expired access token still refreshes,
// and an API key cannot be checked without a billed call, so neither ever
// downgrades validity below unknown.
func (o *opencodeAdapter) readAuth(st *adapter.Status) {
	data, err := os.ReadFile(o.cfg.Abs(authRel))
	if err != nil {
		return
	}
	var entries map[string]struct {
		Type    string  `json:"type"`
		Expires float64 `json:"expires"`
	}
	if json.Unmarshal(data, &entries) != nil || len(entries) == 0 {
		return
	}
	st.Account = strings.Join(slices.Sorted(maps.Keys(entries)), ", ")
	for _, e := range entries {
		if e.Type == "oauth" && e.Expires > 0 && time.UnixMilli(int64(e.Expires)).After(time.Now()) {
			st.Valid = adapter.ValidityValid
			return
		}
	}
}

func (o *opencodeAdapter) Logout(ctx context.Context, src adapter.Source) error {
	switch src {
	case adapter.SourceEnv:
		return common.EnvManagedError(nil)
	case adapter.SourceNone:
		return nil
	}
	// auth logout is an interactive picker; give it a moment in case a
	// non-interactive path exists, then remove the file regardless.
	shortCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, _, _ = o.cfg.Run(shortCtx, "opencode", "auth", "logout")
	if err := common.RemoveFiles(o.cfg.Home, o.Paths()); err != nil {
		return err
	}
	source.ClearRestored(o.cfg.StateDir, o.Name())
	return nil
}

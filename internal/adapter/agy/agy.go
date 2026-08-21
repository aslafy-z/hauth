// Package agy adapts the Antigravity CLI. Credentials live under
// ~/.config/agy (file-based fallback; workspace images ship no keyring).
package agy

import (
	"context"
	"regexp"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/source"
)

const configGlob = ".config/agy/**"

type agyAdapter struct {
	cfg common.Config
}

// New returns the agy adapter.
func New(cfg common.Config) adapter.Adapter {
	return &agyAdapter{cfg: cfg.Complete()}
}

func (a *agyAdapter) Name() string            { return "agy" }
func (a *agyAdapter) Paths() []string         { return []string{configGlob} }
func (a *agyAdapter) IdentityPaths() []string { return a.Paths() }

// No documented env-var short-circuit.
func (a *agyAdapter) EnvAuth() []adapter.EnvAuthSpec { return nil }
func (a *agyAdapter) EnvOverridesFiles() bool        { return false }
func (a *agyAdapter) Refresh() adapter.RefreshPolicy { return adapter.RefreshStable }

func (a *agyAdapter) Methods() []adapter.Method {
	return []adapter.Method{
		{
			ID:    "paste_code",
			Title: "Browser on another device, paste the code",
			Argv:  []string{"agy", "login"},
			// NO_BROWSER nudges the CLI into printing the URL; the BROWSER
			// shim catches it if the CLI tries to open one anyway.
			Env: map[string]string{"NO_BROWSER": "1"},
			Detect: func() adapter.Detector {
				return adapter.NewLineDetector(
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)(?:visit|open).*(?P<url>https://\S+)`), Kind: adapter.EventURL, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)(?:enter|paste).*(?:code|verification)`), Kind: adapter.EventPrompt, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)authentication complete|login successful|logged in`), Kind: adapter.EventSuccess, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)authentication failed|login failed`), Kind: adapter.EventFailure, Once: true},
				)
			},
			Input:   adapter.InputCode,
			Timeout: 15 * time.Minute,
		},
	}
}

// Probe stays cheap in both modes: agy has no status command and a real
// validity check would cost a model call.
func (a *agyAdapter) Probe(ctx context.Context, cheap bool) (adapter.Status, error) {
	src, detail := source.Resolve(a, a.cfg.Home, a.cfg.StateDir)
	st := adapter.Status{Source: src, Detail: detail, Valid: adapter.ValidityUnknown, CheckedAt: time.Now()}
	return st, nil
}

// Logout removes local files only: agy has no documented logout command.
func (a *agyAdapter) Logout(ctx context.Context, src adapter.Source) error {
	switch src {
	case adapter.SourceEnv:
		return common.EnvManagedError(nil)
	case adapter.SourceNone:
		return nil
	}
	if err := common.RemoveFiles(a.cfg.Home, a.Paths()); err != nil {
		return err
	}
	source.ClearRestored(a.cfg.StateDir, a.Name())
	return nil
}

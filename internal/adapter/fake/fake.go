// Package fake adapts the fakecli test harness (testdata/fakecli). It
// exercises every real code path (pty login, detection, bundling, probes,
// logout) without touching a real provider, and backs the end-to-end tests
// and demos. It is never registered by internal/adapter/all.
package fake

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/source"
)

const credRel = ".config/fakecli/credentials.json"

type fakeAdapter struct {
	cfg common.Config
	cli string
}

// New returns the fake adapter driving the fakecli script at cliPath.
func New(cfg common.Config, cliPath string) adapter.Adapter {
	return &fakeAdapter{cfg: cfg.Complete(), cli: cliPath}
}

func (f *fakeAdapter) Name() string            { return "fake" }
func (f *fakeAdapter) Paths() []string         { return []string{credRel} }
func (f *fakeAdapter) IdentityPaths() []string { return f.Paths() }

func (f *fakeAdapter) EnvAuth() []adapter.EnvAuthSpec {
	return []adapter.EnvAuthSpec{{Var: "FAKECLI_TOKEN", Kind: adapter.LongLivedToken}}
}

func (f *fakeAdapter) EnvOverridesFiles() bool        { return true }
func (f *fakeAdapter) Refresh() adapter.RefreshPolicy { return adapter.RefreshStable }

func (f *fakeAdapter) Methods() []adapter.Method {
	return []adapter.Method{
		{
			ID:    "paste_code",
			Title: "Fake paste-code flow",
			Argv:  []string{f.cli, "login"},
			Detect: func() adapter.Detector {
				return adapter.NewLineDetector(
					adapter.Rule{Pattern: regexp.MustCompile(`Open (?P<url>https://\S+)`), Kind: adapter.EventURL, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`Enter code: (?P<code>[A-Z0-9-]+)`), Kind: adapter.EventCode, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`Paste code:`), Kind: adapter.EventPrompt, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`Login successful`), Kind: adapter.EventSuccess, Once: true},
				)
			},
			Input:   adapter.InputCode,
			Timeout: 2 * time.Minute,
		},
	}
}

func (f *fakeAdapter) Probe(ctx context.Context, cheap bool) (adapter.Status, error) {
	src, detail := source.Resolve(f, f.cfg.Home, f.cfg.StateDir)
	st := adapter.Status{Source: src, Detail: detail, Valid: adapter.ValidityUnknown, CheckedAt: time.Now()}
	if src == adapter.SourceNone {
		return st, nil
	}
	if data, err := os.ReadFile(f.cfg.Abs(credRel)); err == nil {
		var c struct {
			Account string `json:"account"`
		}
		if json.Unmarshal(data, &c) == nil {
			st.Account = c.Account
		}
	}
	if cheap {
		return st, nil
	}
	_, _, code, err := f.cfg.Run(ctx, f.cli, "status")
	if err == nil {
		if code == 0 {
			st.Valid = adapter.ValidityValid
		} else {
			st.Valid = adapter.ValidityInvalid
		}
	}
	return st, nil
}

func (f *fakeAdapter) Logout(ctx context.Context, src adapter.Source) error {
	switch src {
	case adapter.SourceEnv:
		return common.EnvManagedError([]string{"FAKECLI_TOKEN"})
	case adapter.SourceNone:
		return nil
	}
	_, _, _, _ = f.cfg.Run(ctx, f.cli, "logout")
	if err := common.RemoveFiles(f.cfg.Home, f.Paths()); err != nil {
		return err
	}
	source.ClearRestored(f.cfg.StateDir, f.Name())
	return nil
}

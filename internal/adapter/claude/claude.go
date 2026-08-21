// Package claude adapts Claude Code. Credentials live in
// ~/.claude/.credentials.json; ~/.claude.json is synced as a filtered copy
// holding only onboarding and account keys so project history never enters
// the store.
package claude

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

const (
	credentialsRel = ".claude/.credentials.json"
	settingsRel    = ".claude.json"
)

// settingsKeep lists the ~/.claude.json keys that are synced: onboarding
// state (skips the first-run wizard) and account identity. Everything else,
// notably per-project history, stays local.
var settingsKeep = []string{
	"hasCompletedOnboarding",
	"oauthAccount",
	"userID",
	"installMethod",
	"autoUpdates",
	"theme",
}

type claudeAdapter struct {
	cfg common.Config
}

// New returns the claude adapter.
func New(cfg common.Config) adapter.Adapter {
	return &claudeAdapter{cfg: cfg.Complete()}
}

func (c *claudeAdapter) Name() string            { return "claude" }
func (c *claudeAdapter) Paths() []string         { return []string{credentialsRel, settingsRel} }
func (c *claudeAdapter) IdentityPaths() []string { return []string{credentialsRel} }

func (c *claudeAdapter) EnvAuth() []adapter.EnvAuthSpec {
	return []adapter.EnvAuthSpec{
		{Var: "CLAUDE_CODE_OAUTH_TOKEN", Kind: adapter.LongLivedToken},
		{Var: "ANTHROPIC_API_KEY", Kind: adapter.APIKey},
	}
}

func (c *claudeAdapter) EnvOverridesFiles() bool        { return true }
func (c *claudeAdapter) Refresh() adapter.RefreshPolicy { return adapter.RefreshRotating }

func pasteDetector() adapter.Detector {
	return adapter.NewLineDetector(
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)paste code`), Kind: adapter.EventPrompt, Once: true},
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)login successful|logged in`), Kind: adapter.EventSuccess, Once: true},
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)invalid code|login failed|unable to log ?in`), Kind: adapter.EventFailure, Once: true},
	)
}

func (c *claudeAdapter) Methods() []adapter.Method {
	return []adapter.Method{
		{
			ID:           "paste_code",
			Title:        "Browser on another device, paste the code",
			Argv:         []string{"claude", "/login"},
			Detect:       pasteDetector,
			Input:        adapter.InputCode,
			Timeout:      15 * time.Minute,
			EndOnSuccess: true, // /login returns to the REPL, which never exits
		},
		{
			// setup-token runs the same paste-code dance and then prints a
			// long-lived token for CLAUDE_CODE_OAUTH_TOKEN.
			ID:      "token",
			Title:   "Create a long-lived token (setup-token)",
			Argv:    []string{"claude", "setup-token"},
			Detect:  pasteDetector,
			Input:   adapter.InputCode,
			Timeout: 15 * time.Minute,
		},
	}
}

// Probe never makes a network call regardless of cheap: a real validity
// check would cost a model invocation. Validity comes from the cached OAuth
// expiry timestamp instead.
func (c *claudeAdapter) Probe(ctx context.Context, cheap bool) (adapter.Status, error) {
	src, detail := source.Resolve(c, c.cfg.Home, c.cfg.StateDir)
	st := adapter.Status{Source: src, Detail: detail, Valid: adapter.ValidityUnknown, CheckedAt: time.Now()}
	if src == adapter.SourceNone {
		return st, nil
	}
	if data, err := os.ReadFile(c.cfg.Abs(credentialsRel)); err == nil {
		var f struct {
			ClaudeAiOauth struct {
				ExpiresAt float64 `json:"expiresAt"` // ms epoch
			} `json:"claudeAiOauth"`
		}
		if json.Unmarshal(data, &f) == nil && f.ClaudeAiOauth.ExpiresAt > 0 {
			if time.UnixMilli(int64(f.ClaudeAiOauth.ExpiresAt)).After(time.Now()) {
				st.Valid = adapter.ValidityValid
			} else {
				st.Valid = adapter.ValidityInvalid
			}
		}
	}
	if data, err := os.ReadFile(c.cfg.Abs(settingsRel)); err == nil {
		var f struct {
			OauthAccount struct {
				EmailAddress string `json:"emailAddress"`
			} `json:"oauthAccount"`
		}
		if json.Unmarshal(data, &f) == nil {
			st.Account = f.OauthAccount.EmailAddress
		}
	}
	return st, nil
}

// Logout removes local credential files. Claude Code has no non-interactive
// logout command; provider-side revocation happens on next login or through
// the account console.
func (c *claudeAdapter) Logout(ctx context.Context, src adapter.Source) error {
	switch src {
	case adapter.SourceEnv:
		return common.EnvManagedError([]string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"})
	case adapter.SourceNone:
		return nil
	}
	if err := common.RemoveFiles(c.cfg.Home, []string{credentialsRel}); err != nil {
		return err
	}
	source.ClearRestored(c.cfg.StateDir, c.Name())
	return nil
}

// FilterFile keeps only the allowlisted keys of ~/.claude.json; the
// credentials file passes through unchanged.
func (c *claudeAdapter) FilterFile(rel string, content []byte) ([]byte, bool) {
	if rel != settingsRel {
		return content, true
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(content, &m); err != nil {
		return nil, false
	}
	out := map[string]json.RawMessage{}
	for _, k := range settingsKeep {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, false
	}
	return append(data, '\n'), true
}

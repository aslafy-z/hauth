// Package claude adapts Claude Code. Credentials live in
// ~/.claude/.credentials.json; ~/.claude.json is synced as a filtered copy
// holding only onboarding and account keys so project history never enters
// the store.
package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
		// "logged in" alone would also match the REPL status bar's "Not
		// logged in · Run /login"; the bundle's success strings are "Login
		// successful" and "Logged in as".
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)login successful|logged in as`), Kind: adapter.EventSuccess, Once: true},
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)invalid code|login failed|unable to log ?in`), Kind: adapter.EventFailure, Once: true},
		// First-run onboarding gates, each accepted with its default so
		// nobody has to navigate the TUI by hand: theme picker, login
		// method (subscription account is preselected; the token method
		// exists for console accounts), security notes, folder trust.
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)choose (your|the) (color scheme|text style)`), Kind: adapter.EventPrompt, Once: true, Respond: "\r"},
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)select login method`), Kind: adapter.EventPrompt, Once: true, Respond: "\r"},
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)press enter to continue`), Kind: adapter.EventPrompt, Once: true, Respond: "\r"},
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)do you trust the files`), Kind: adapter.EventPrompt, Once: true, Respond: "\r"},
		// Fallback for the 2.x folder-trust dialog when trustProjectDir
		// could not pre-seed it away. It preselects "No, exit" for folders
		// it finds suspicious, so a blind Enter can kill the login. Answer
		// by pointer position: confirm when the pointer sits on Yes; send
		// down-then-Enter in one write when it sits on No (the post-move
		// redraw only repaints the pointer cell, never the option text, so
		// a second rule would never see the pointer land on Yes).
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)[❯›>]\s*(?:1\.\s*)?yes, i trust`), Kind: adapter.EventPrompt, Once: true, Respond: "\r"},
		adapter.Rule{Pattern: regexp.MustCompile(`(?i)[❯›>]\s*(?:\d\.\s*)?no, exit`), Kind: adapter.EventPrompt, Once: true, Respond: "\x1b[B\r"},
	)
}

// trustProjectDir marks cwd as trusted in ~/.claude.json so the
// folder-trust dialog never gates the login TUI (the same pre-seeding the
// IDE integrations use). Best-effort: an unreadable or corrupt settings
// file is left alone and the detector's dialog fallback takes over. The
// projects key stays local; FilterFile excludes it from the synced copy.
func trustProjectDir(home, cwd string) error {
	if cwd == "" {
		return nil
	}
	path := filepath.Join(home, settingsRel)
	m := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &m) != nil {
			return nil
		}
	}
	projects := map[string]map[string]any{}
	if raw, ok := m["projects"]; ok {
		if json.Unmarshal(raw, &projects) != nil {
			return nil
		}
	}
	if projects[cwd] == nil {
		projects[cwd] = map[string]any{}
	}
	projects[cwd]["hasTrustDialogAccepted"] = true
	raw, err := json.Marshal(projects)
	if err != nil {
		return err
	}
	m["projects"] = raw
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
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
			Prepare:      trustProjectDir,
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
			Prepare: trustProjectDir,
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

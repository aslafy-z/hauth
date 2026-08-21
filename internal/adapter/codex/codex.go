// Package codex adapts the OpenAI Codex CLI. Credentials live in
// ~/.codex/auth.json; device auth is the default browser-free flow.
package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/source"
)

const authRel = ".codex/auth.json"

type codexAdapter struct {
	cfg common.Config
}

// New returns the codex adapter.
func New(cfg common.Config) adapter.Adapter {
	return &codexAdapter{cfg: cfg.Complete()}
}

func (c *codexAdapter) Name() string            { return "codex" }
func (c *codexAdapter) Paths() []string         { return []string{authRel} }
func (c *codexAdapter) IdentityPaths() []string { return c.Paths() }

func (c *codexAdapter) EnvAuth() []adapter.EnvAuthSpec {
	return []adapter.EnvAuthSpec{{Var: "OPENAI_API_KEY", Kind: adapter.APIKey}}
}

func (c *codexAdapter) EnvOverridesFiles() bool        { return true }
func (c *codexAdapter) Refresh() adapter.RefreshPolicy { return adapter.RefreshRotating }

func (c *codexAdapter) Methods() []adapter.Method {
	return []adapter.Method{
		{
			ID:    "device",
			Title: "Device code",
			Argv:  []string{"codex", "login", "--device-auth"},
			Detect: func() adapter.Detector {
				return adapter.NewLineDetector(
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)(?:go to|visit)[:\s]+(?P<url>https://\S+)`), Kind: adapter.EventURL, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)code[:\s]+(?P<code>[A-Z0-9]{4}-[A-Z0-9]{4,})`), Kind: adapter.EventCode, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)successfully logged in|logged in as`), Kind: adapter.EventSuccess, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)login failed|device code expired`), Kind: adapter.EventFailure, Once: true},
				)
			},
			Input:   adapter.InputNone,
			Timeout: 15 * time.Minute,
		},
		{
			ID:      "api_key",
			Title:   "Paste an OpenAI API key",
			Argv:    []string{"codex", "login", "--with-api-key"},
			Input:   adapter.InputToken,
			Timeout: 5 * time.Minute,
		},
	}
}

var emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

func (c *codexAdapter) Probe(ctx context.Context, cheap bool) (adapter.Status, error) {
	src, detail := source.Resolve(c, c.cfg.Home, c.cfg.StateDir)
	st := adapter.Status{Source: src, Detail: detail, Valid: adapter.ValidityUnknown, CheckedAt: time.Now()}
	if src == adapter.SourceNone {
		return st, nil
	}
	st.Account = authEmail(c.cfg.Abs(authRel))
	if cheap {
		return st, nil
	}
	stdout, stderr, code, err := c.cfg.Run(ctx, "codex", "login", "status")
	if err != nil {
		return st, nil
	}
	if code == 0 {
		st.Valid = adapter.ValidityValid
		if st.Account == "" {
			if m := emailRe.FindString(stdout + stderr); m != "" {
				st.Account = m
			}
		}
	} else {
		st.Valid = adapter.ValidityInvalid
	}
	return st, nil
}

// authEmail extracts the account email from the id_token JWT payload,
// best-effort and without validation; it is display metadata only.
func authEmail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var f struct {
		Tokens struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &f) != nil || f.Tokens.IDToken == "" {
		return ""
	}
	parts := strings.Split(f.Tokens.IDToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Email
}

func (c *codexAdapter) Logout(ctx context.Context, src adapter.Source) error {
	switch src {
	case adapter.SourceEnv:
		return common.EnvManagedError([]string{"OPENAI_API_KEY"})
	case adapter.SourceNone:
		return nil
	}
	_, _, _, _ = c.cfg.Run(ctx, "codex", "logout")
	if err := common.RemoveFiles(c.cfg.Home, c.Paths()); err != nil {
		return err
	}
	source.ClearRestored(c.cfg.StateDir, c.Name())
	return nil
}

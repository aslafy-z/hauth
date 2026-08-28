// Package gh adapts the GitHub CLI. Credentials live in hosts.yml; device
// flow is the default login; GH_TOKEN and GITHUB_TOKEN short-circuit it.
package gh

import (
	"context"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/source"
)

type ghAdapter struct {
	cfg common.Config
}

// New returns the gh adapter.
func New(cfg common.Config) adapter.Adapter {
	return &ghAdapter{cfg: cfg.Complete()}
}

func (g *ghAdapter) Name() string            { return "gh" }
func (g *ghAdapter) Paths() []string         { return []string{".config/gh/hosts.yml"} }
func (g *ghAdapter) IdentityPaths() []string { return g.Paths() }

func (g *ghAdapter) EnvAuth() []adapter.EnvAuthSpec {
	return []adapter.EnvAuthSpec{
		{Var: "GH_TOKEN", Kind: adapter.LongLivedToken},
		{Var: "GITHUB_TOKEN", Kind: adapter.LongLivedToken},
	}
}

func (g *ghAdapter) EnvOverridesFiles() bool        { return true }
func (g *ghAdapter) Refresh() adapter.RefreshPolicy { return adapter.RefreshStable }

func (g *ghAdapter) Methods() []adapter.Method {
	return []adapter.Method{
		{
			ID:    "device",
			Title: "Device flow",
			Argv: []string{
				"gh", "auth", "login",
				"--hostname", "github.com",
				"--web", "--git-protocol", "https", "--skip-ssh-key",
			},
			Detect: func() adapter.Detector {
				return adapter.NewLineDetector(
					adapter.Rule{Pattern: regexp.MustCompile(`one-time code: (?P<code>[A-Z0-9]{4}-[A-Z0-9]{4})`), Kind: adapter.EventCode, Once: true},
					// gh gates the device flow behind an Enter press;
					// answer it so the flow proceeds without the user
					// having to open the terminal (the "browser" it then
					// opens is the session's shim).
					adapter.Rule{Pattern: regexp.MustCompile(`Press Enter to open .* in your browser`), Kind: adapter.EventPrompt, Once: true, Respond: "\r"},
					// gh asks whether to configure the git credential
					// helper before starting the flow; accept the default
					// Yes, which a workspace wants anyway.
					adapter.Rule{Pattern: regexp.MustCompile(`Authenticate Git with your GitHub credentials`), Kind: adapter.EventPrompt, Once: true, Respond: "\r"},
					adapter.Rule{Pattern: regexp.MustCompile(`(?P<url>https://github\.com/login/device[^\s"']*)`), Kind: adapter.EventURL, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`Logged in as \S+`), Kind: adapter.EventSuccess, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`authorization request was denied|Authentication failed`), Kind: adapter.EventFailure, Once: true},
				)
			},
			Input:   adapter.InputNone,
			Timeout: 15 * time.Minute,
		},
		{
			ID:      "token",
			Title:   "Paste a personal access token",
			Argv:    []string{"gh", "auth", "login", "--hostname", "github.com", "--with-token"},
			Input:   adapter.InputToken,
			Timeout: 5 * time.Minute,
		},
	}
}

var statusAcctRe = regexp.MustCompile(`account (\S+)|Logged in to github\.com as (\S+)`)

func (g *ghAdapter) Probe(ctx context.Context, cheap bool) (adapter.Status, error) {
	src, detail := source.Resolve(g, g.cfg.Home, g.cfg.StateDir)
	st := adapter.Status{Source: src, Detail: detail, Valid: adapter.ValidityUnknown, CheckedAt: time.Now()}
	if src == adapter.SourceNone {
		return st, nil
	}
	if cheap {
		st.Account = hostsAccount(g.cfg.Abs(".config/gh/hosts.yml"))
		return st, nil
	}
	stdout, stderr, code, err := g.cfg.Run(ctx, "gh", "auth", "status", "--hostname", "github.com")
	if err != nil {
		// gh not installed or not runnable: leave validity unknown.
		return st, nil
	}
	combined := stdout + stderr
	if code == 0 {
		st.Valid = adapter.ValidityValid
		if m := statusAcctRe.FindStringSubmatch(combined); m != nil {
			if m[1] != "" {
				st.Account = m[1]
			} else {
				st.Account = m[2]
			}
		}
	} else {
		st.Valid = adapter.ValidityInvalid
	}
	return st, nil
}

// hostsAccount extracts the account name from hosts.yml without a YAML
// dependency; the file is small and machine-written.
func hostsAccount(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(trimmed, "user:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (g *ghAdapter) Logout(ctx context.Context, src adapter.Source) error {
	switch src {
	case adapter.SourceEnv:
		return common.EnvManagedError([]string{"GH_TOKEN", "GITHUB_TOKEN"})
	case adapter.SourceNone:
		return nil
	}
	// Provider-side revocation first, while the token is still readable.
	_, _, _, _ = g.cfg.Run(ctx, "gh", "auth", "logout", "--hostname", "github.com")
	if err := common.RemoveFiles(g.cfg.Home, g.Paths()); err != nil {
		return err
	}
	source.ClearRestored(g.cfg.StateDir, g.Name())
	return nil
}

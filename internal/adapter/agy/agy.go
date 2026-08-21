// Package agy adapts the Antigravity CLI.
//
// agy has no login/logout/status subcommands: authentication happens inside
// its TUI, which runs a browser OAuth flow with a loopback callback. Tokens
// land in the OS keyring when a D-Bus session is available; on headless
// hosts and containers the CLI bypasses the keyring and stores tokens in a
// token directory under its app data dir (~/.gemini/antigravity-cli).
//
// Validity cannot be read from files or exit codes: print mode succeeds
// even unauthenticated (anonymous tier). The reliable signal is the CLI's
// own log, which states either the authenticated account or "You are not
// logged into Antigravity", so the expensive probe runs a one-token prompt
// with --log-file pointed at a scratch file and parses that.
package agy

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/source"
)

// tokensGlob is the CLI's file token storage, used when no keyring is
// available. Inferred from the CLI's cliFileTokenStorage internals; kept
// narrow on purpose so conversations, caches, and logs never sync.
const tokensGlob = ".gemini/antigravity-cli/tokens/**"

type agyAdapter struct {
	cfg common.Config
}

// New returns the agy adapter.
func New(cfg common.Config) adapter.Adapter {
	return &agyAdapter{cfg: cfg.Complete()}
}

func (a *agyAdapter) Name() string            { return "agy" }
func (a *agyAdapter) Paths() []string         { return []string{tokensGlob} }
func (a *agyAdapter) IdentityPaths() []string { return a.Paths() }

// No documented env-var short-circuit.
func (a *agyAdapter) EnvAuth() []adapter.EnvAuthSpec { return nil }
func (a *agyAdapter) EnvOverridesFiles() bool        { return false }
func (a *agyAdapter) Refresh() adapter.RefreshPolicy { return adapter.RefreshStable }

func (a *agyAdapter) Methods() []adapter.Method {
	return []adapter.Method{
		{
			ID:    "interactive",
			Title: "Sign in inside the agy terminal UI",
			// Any unknown subcommand also lands in the TUI; run it plainly.
			// First run walks through onboarding and sign-in; the browser
			// URL is captured by the session's BROWSER shim.
			Argv: []string{"agy"},
			Detect: func() adapter.Detector {
				return &tuiDetector{inner: adapter.NewLineDetector(
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)not signed in`), Kind: adapter.EventPrompt, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)(?:visit|open|go to).*(?P<url>https://\S+)`), Kind: adapter.EventURL, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)authentication successful|signed in as|login successful`), Kind: adapter.EventSuccess, Once: true},
					adapter.Rule{Pattern: regexp.MustCompile(`(?i)authentication failed or timed out|login failed`), Kind: adapter.EventFailure, Once: true},
				)}
			},
			Input:        adapter.InputFreeText,
			Timeout:      15 * time.Minute,
			EndOnSuccess: true, // the TUI stays open after signing in
		},
	}
}

var (
	reAuthEmail   = regexp.MustCompile(`applyAuthResult: email=([^,\s]+)`)
	reNotLoggedIn = regexp.MustCompile(`You are not logged into Antigravity`)
	reViaKeyring  = regexp.MustCompile(`authenticated via keyring`)
	reHeaderEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
)

// tuiDetector wraps the rule detector with one signal rules cannot express
// (RE2 has no lookahead): once signed in, the TUI header shows the account
// email. An email on a line that is not a URL counts as success.
type tuiDetector struct {
	inner    *adapter.LineDetector
	sawEmail bool
}

func (d *tuiDetector) Feed(line string) []adapter.Event {
	evs := d.inner.Feed(line)
	if !d.sawEmail && !strings.Contains(line, "://") && reHeaderEmail.MatchString(line) {
		d.sawEmail = true
		evs = append(evs, adapter.Event{Kind: adapter.EventSuccess, Prompt: line})
	}
	return evs
}

func (d *tuiDetector) FeedPartial(line string) []adapter.Event {
	return d.inner.FeedPartial(line)
}

// parseAuthLog classifies a CLI log capture. account is empty when the log
// shows no authenticated identity.
func parseAuthLog(log string) (account string, valid adapter.Validity, keyring bool) {
	if m := reAuthEmail.FindStringSubmatch(log); m != nil {
		return m[1], adapter.ValidityValid, reViaKeyring.MatchString(log)
	}
	if reNotLoggedIn.MatchString(log) {
		return "", adapter.ValidityInvalid, false
	}
	return "", adapter.ValidityUnknown, false
}

func (a *agyAdapter) Probe(ctx context.Context, cheap bool) (adapter.Status, error) {
	src, detail := source.Resolve(a, a.cfg.Home, a.cfg.StateDir)
	st := adapter.Status{Source: src, Detail: detail, Valid: adapter.ValidityUnknown, CheckedAt: time.Now()}
	if cheap {
		return st, nil
	}

	logFile, err := os.CreateTemp("", "hauth-agy-*.log")
	if err != nil {
		return st, err
	}
	logPath := logFile.Name()
	logFile.Close()
	defer os.Remove(logPath)

	// One minimal model call; anonymous fallback means the exit code and
	// reply prove nothing, only the log does.
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	_, _, _, runErr := a.cfg.Run(runCtx, "agy",
		"--log-file", logPath,
		"--print-timeout", "45s",
		"-p", "Reply with exactly: ok")
	logData, readErr := os.ReadFile(logPath)
	if readErr != nil && runErr != nil {
		return st, runErr
	}

	account, valid, keyring := parseAuthLog(string(logData))
	st.Valid = valid
	st.Account = account
	st.CheckedAt = time.Now()
	if valid == adapter.ValidityValid && src == adapter.SourceNone {
		// Authenticated with no credential files in view: the token lives
		// in the OS keyring (or somewhere hauth does not manage). Report
		// it as managed externally rather than "not logged in".
		st.Source = adapter.SourceFileExternal
		if keyring {
			st.Detail = "OS keyring (not syncable)"
		} else {
			st.Detail = "credentials outside managed paths"
		}
	}
	if valid == adapter.ValidityInvalid && src == adapter.SourceNone {
		st.Detail = "not logged in"
	}
	return st, nil
}

// Logout removes the file token storage; keyring-held credentials cannot be
// removed from here.
func (a *agyAdapter) Logout(ctx context.Context, src adapter.Source) error {
	switch src {
	case adapter.SourceEnv:
		return common.EnvManagedError(nil)
	case adapter.SourceNone:
		return nil
	}
	tokensDir := filepath.Join(a.cfg.Home, ".gemini", "antigravity-cli", "tokens")
	if _, err := os.Stat(tokensDir); err == nil {
		if err := os.RemoveAll(tokensDir); err != nil {
			return err
		}
	}
	source.ClearRestored(a.cfg.StateDir, a.Name())
	return nil
}

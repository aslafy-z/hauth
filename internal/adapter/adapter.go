// Package adapter defines the contract between hauth and each harness CLI.
//
// An adapter declares where a harness keeps credentials, which environment
// variables short-circuit its interactive login, how to run its browser-free
// login flows, and how to probe and revoke its authentication state. Adapters
// never talk to the store; orchestration lives in internal/agent.
package adapter

import (
	"context"
	"time"
)

// Source describes where a harness's current authentication comes from.
type Source int

const (
	// SourceNone means no authentication material was found.
	SourceNone Source = iota
	// SourceStore means local files match a bundle hauth restored.
	SourceStore
	// SourceFileExternal means credential files exist but were not written
	// by hauth (mounted secret, dotfiles, manual login).
	SourceFileExternal
	// SourceEnv means an environment variable honored by the CLI is set.
	// hauth did not put it there and cannot remove it.
	SourceEnv
)

func (s Source) String() string {
	switch s {
	case SourceStore:
		return "store"
	case SourceFileExternal:
		return "file-external"
	case SourceEnv:
		return "env"
	default:
		return "none"
	}
}

// Validity is the result of checking whether credentials still work. It is
// deliberately separate from Source: files can be present yet expired.
type Validity int

const (
	ValidityUnknown Validity = iota
	ValidityValid
	ValidityInvalid
)

func (v Validity) String() string {
	switch v {
	case ValidityValid:
		return "valid"
	case ValidityInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// RefreshPolicy describes how a harness's credential files evolve over time
// and therefore how the store synchronizes them.
type RefreshPolicy int

const (
	// RefreshNone: nothing on disk to sync (env-injected tokens only).
	RefreshNone RefreshPolicy = iota
	// RefreshStable: files change only on explicit login/logout. Everyone
	// restores, last writer wins, push on content change.
	RefreshStable
	// RefreshRotating: the CLI rewrites tokens on its own. Exactly one
	// workspace holds the lease and pushes; others restore once.
	RefreshRotating
)

func (r RefreshPolicy) String() string {
	switch r {
	case RefreshStable:
		return "stable"
	case RefreshRotating:
		return "rotating"
	default:
		return "none"
	}
}

// InputKind tells the kiosk what input widget a login method needs.
type InputKind int

const (
	InputNone InputKind = iota
	InputCode
	InputToken
	InputFreeText
)

func (k InputKind) String() string {
	switch k {
	case InputCode:
		return "code"
	case InputToken:
		return "token"
	case InputFreeText:
		return "free_text"
	default:
		return "none"
	}
}

// TokenKind classifies an env-var credential.
type TokenKind int

const (
	TokenUnknown TokenKind = iota
	// LongLivedToken is expected to work until revoked (PATs, setup tokens).
	LongLivedToken
	// APIKey is a provider API key rather than an OAuth credential.
	APIKey
)

// EnvAuthSpec names an environment variable that, when set, makes the CLI
// skip interactive login.
type EnvAuthSpec struct {
	Var  string
	Kind TokenKind
}

// Status is the resolved authentication state of one harness.
type Status struct {
	Source    Source
	Detail    string // e.g. "env GH_TOKEN", "files present", "store rev 12"
	Valid     Validity
	Account   string // only if the CLI exposes it cheaply
	CheckedAt time.Time
}

// Method is one way to log a harness in. Methods are ordered; the first is
// the default. Argv is executed in a pty by internal/session.
type Method struct {
	ID      string // "device", "paste_code", "token", "external_auth"
	Title   string // human label for the kiosk
	Argv    []string
	Env     map[string]string // BROWSER shim, TERM, forced-remote hints
	Cwd     string
	Detect  func() Detector // fresh detector per session; nil means URL-regex only
	Input   InputKind
	Timeout time.Duration
	// EndOnSuccess ends the session shortly after a detected success.
	// Needed for CLIs whose login happens inside a REPL or TUI that never
	// exits on its own (claude, agy).
	EndOnSuccess bool
}

// Adapter is implemented once per harness CLI.
type Adapter interface {
	Name() string

	// Paths returns the credential material this adapter owns, as globs
	// relative to $HOME. These are the only paths bundled and restored.
	Paths() []string

	// IdentityPaths returns the subset of Paths whose hashes participate in
	// source detection. Files outside this subset (onboarding flags, UI
	// state the CLI rewrites on its own) never flip a harness from "store"
	// to "file-external" on their own.
	IdentityPaths() []string

	// EnvAuth lists env vars that make the CLI skip interactive login,
	// most specific first.
	EnvAuth() []EnvAuthSpec

	// EnvOverridesFiles reports whether a set env var beats credential
	// files for this CLI. When true, "log in anyway" is pointless while
	// the env var is set and the kiosk hides it.
	EnvOverridesFiles() bool

	Methods() []Method

	// Probe resolves the current Status. With cheap=true it must not make
	// a network call; validity may come back ValidityUnknown.
	Probe(ctx context.Context, cheap bool) (Status, error)

	Refresh() RefreshPolicy

	// Logout revokes and removes credentials appropriate for the given
	// source. It must attempt CLI-native logout first (provider-side
	// revocation needs the token to still be readable), then remove files.
	// Store cleanup is the caller's job.
	Logout(ctx context.Context, src Source) error
}

// FileFilter is optionally implemented by adapters whose files must be
// filtered before entering a bundle (e.g. ~/.claude.json keeps onboarding
// and account keys, drops project history). Returning ok=false excludes the
// file from the bundle entirely.
type FileFilter interface {
	FilterFile(rel string, content []byte) (out []byte, ok bool)
}

// Env returns the ordered env var names from EnvAuth.
func EnvVars(a Adapter) []string {
	specs := a.EnvAuth()
	vars := make([]string, 0, len(specs))
	for _, s := range specs {
		vars = append(vars, s.Var)
	}
	return vars
}

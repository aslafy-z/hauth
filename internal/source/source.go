// Package source resolves where a harness's current authentication comes
// from: env, file-external, store, or none. The resolution order and the
// "managed externally" semantics are the core of hauth's do-no-harm rule.
package source

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/bundle"
)

// StateDir returns the directory where hauth records what it restored.
func StateDir() string {
	if dir := os.Getenv("HAUTH_STATE_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "hauth")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "hauth-state")
	}
	return filepath.Join(home, ".local", "state", "hauth")
}

func restoredPath(stateDir, harness string) string {
	return filepath.Join(stateDir, harness+".restored.json")
}

// SaveRestored records the manifest hauth just restored (or pushed) for a
// harness. The agent calls this after every restore and after every push so
// the holder's own rotations keep resolving to SourceStore.
func SaveRestored(stateDir string, m bundle.Manifest) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(restoredPath(stateDir, m.Harness), data, 0o600)
}

// LoadRestored returns the last restored manifest for harness, or ok=false.
func LoadRestored(stateDir, harness string) (bundle.Manifest, bool) {
	data, err := os.ReadFile(restoredPath(stateDir, harness))
	if err != nil {
		return bundle.Manifest{}, false
	}
	var m bundle.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return bundle.Manifest{}, false
	}
	return m, true
}

// ClearRestored forgets the restored record for harness (after logout or
// store deletion).
func ClearRestored(stateDir, harness string) {
	_ = os.Remove(restoredPath(stateDir, harness))
}

// Resolve determines the Source for a harness. Order:
//
//  1. env: an env var the CLI honors is set (hauth did not put it there),
//  2. file-external: identity files exist but do not match what hauth
//     restored,
//  3. store: identity files match the restored manifest,
//  4. none.
func Resolve(a adapter.Adapter, home, stateDir string) (adapter.Source, string) {
	for _, spec := range a.EnvAuth() {
		if os.Getenv(spec.Var) != "" {
			return adapter.SourceEnv, "env " + spec.Var
		}
	}
	identity := a.IdentityPaths()
	local, err := bundle.ExpandGlobs(home, identity)
	if err != nil || len(local) == 0 {
		return adapter.SourceNone, "no credentials"
	}
	if m, ok := LoadRestored(stateDir, a.Name()); ok {
		if m.IdentityMatchesLocal(home, identity) {
			return adapter.SourceStore, "files match store bundle"
		}
	}
	return adapter.SourceFileExternal, "files present, not written by hauth"
}

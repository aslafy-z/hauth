// Package installer installs harness CLIs natively: GitHub release
// binaries downloaded into ~/.local/bin, or the harness's official install
// script. No package manager is required; strategies that need external
// tools (bash for scripts, npm as a last resort) are skipped when those
// tools are missing.
package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

// strategy is one way to install a harness CLI. Strategies are tried in
// order; the first whose prerequisites are met runs.
type strategy interface {
	// describe is a short human-readable label for logs.
	describe() string
	// ready reports whether the strategy's prerequisites are on PATH.
	ready() bool
	// run performs the installation, streaming progress through logf.
	run(ctx context.Context, logf func(string, ...any)) error
}

type spec struct {
	// Binary is the executable the install is expected to provide.
	Binary     string
	Strategies []strategy
	// ExtraDirs lists home-relative directories where an official install
	// script may have placed the binary outside ~/.local/bin.
	ExtraDirs []string
}

// specs holds the install sources verified against each project's official
// documentation or release listing; see docs/ops.md for the references.
var specs = map[string]spec{
	"gh": {
		Binary: "gh",
		Strategies: []strategy{
			&releaseInstall{
				Repo:        "cli/cli",
				InstallName: "gh",
				Assets: map[string]string{
					"linux/amd64":  "gh_*_linux_amd64.tar.gz",
					"linux/arm64":  "gh_*_linux_arm64.tar.gz",
					"darwin/amd64": "gh_*_macOS_amd64.zip",
					"darwin/arm64": "gh_*_macOS_arm64.zip",
				},
			},
		},
	},
	"claude": {
		Binary: "claude",
		Strategies: []strategy{
			&scriptInstall{URL: "https://claude.ai/install.sh"},
			&npmInstall{Package: "@anthropic-ai/claude-code"},
		},
	},
	"codex": {
		Binary: "codex",
		Strategies: []strategy{
			&releaseInstall{
				Repo:        "openai/codex",
				InstallName: "codex",
				Assets: map[string]string{
					"linux/amd64":  "codex-x86_64-unknown-linux-musl.tar.gz",
					"linux/arm64":  "codex-aarch64-unknown-linux-musl.tar.gz",
					"darwin/amd64": "codex-x86_64-apple-darwin.tar.gz",
					"darwin/arm64": "codex-aarch64-apple-darwin.tar.gz",
				},
			},
		},
	},
	"opencode": {
		Binary: "opencode",
		// The release binary lands in ~/.local/bin like everything else;
		// the official script is the fallback but installs into
		// ~/.opencode/bin, hence the extra lookup dir.
		Strategies: []strategy{
			&releaseInstall{
				Repo:        "anomalyco/opencode",
				InstallName: "opencode",
				Assets: map[string]string{
					"linux/amd64":  "opencode-linux-x64.tar.gz",
					"linux/arm64":  "opencode-linux-arm64.tar.gz",
					"darwin/amd64": "opencode-darwin-x64.zip",
					"darwin/arm64": "opencode-darwin-arm64.zip",
				},
			},
			&scriptInstall{URL: "https://opencode.ai/install"},
		},
		ExtraDirs: []string{".opencode/bin"},
	},
	"agy": {
		Binary: "agy",
		Strategies: []strategy{
			&scriptInstall{URL: "https://antigravity.google/cli/install.sh"},
		},
	},
	// copilot has no adapter (hauth does not broker its auth); the installer
	// mapping exists so workspaces can provision the CLI for HAPI's Copilot
	// agent. npm is its only documented install channel.
	"copilot": {
		Binary: "copilot",
		Strategies: []strategy{
			&npmInstall{Package: "@github/copilot"},
		},
	},
}

// Known reports whether the harness has an installer mapping.
func Known(harness string) bool {
	_, ok := specs[harness]
	return ok
}

// BinaryFor returns the executable name a harness's CLI installs as.
func BinaryFor(harness string) (string, bool) {
	s, ok := specs[harness]
	if !ok {
		return "", false
	}
	return s.Binary, true
}

// Installed reports whether the harness's CLI is on PATH. Unmapped
// harnesses report false.
func Installed(harness string) bool {
	s, ok := specs[harness]
	if !ok {
		return false
	}
	if _, err := exec.LookPath(s.Binary); err == nil {
		return true
	}
	// A fresh install lands in ~/.local/bin (or a script's own directory),
	// which may not be on PATH yet in this process; count it as installed
	// so the UI does not offer an endless Install button.
	if dir, err := binDir(); err == nil {
		if info, err := os.Stat(filepath.Join(dir, s.Binary)); err == nil && !info.IsDir() {
			return true
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, rel := range s.ExtraDirs {
			if info, err := os.Stat(filepath.Join(home, rel, s.Binary)); err == nil && !info.IsDir() {
				return true
			}
		}
	}
	return false
}

// Available reports whether installation is possible at all. Release
// downloads only need network access, so this is always true; it remains a
// function so the kiosk gating has one place to ask.
func Available() bool {
	return true
}

// binDir returns the directory binaries are installed into. Overridable in
// tests.
var binDir = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin"), nil
}

// Install installs one harness CLI using the first ready strategy.
func Install(ctx context.Context, harness string, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s, ok := specs[harness]
	if !ok {
		return fmt.Errorf("no installer for harness %q", harness)
	}
	var lastErr error
	tried := false
	for _, st := range s.Strategies {
		if !st.ready() {
			logf("install %s: skipping %s (prerequisites missing)", harness, st.describe())
			continue
		}
		tried = true
		logf("install %s: %s", harness, st.describe())
		if err := st.run(ctx, func(format string, args ...any) {
			logf("install %s: "+format, append([]any{harness}, args...)...)
		}); err != nil {
			lastErr = err
			logf("install %s: %s failed: %v", harness, st.describe(), err)
			continue
		}
		verify(ctx, s.Binary, logf)
		warnPath(logf)
		return nil
	}
	if !tried {
		return fmt.Errorf("no usable install method for %q (a POSIX shell is required for script installs)", harness)
	}
	return fmt.Errorf("install %s: %w", harness, lastErr)
}

// InstallAll installs the given harnesses sequentially, streaming progress
// through logf. A failing harness does not stop the others; the joined
// error is returned at the end.
func InstallAll(ctx context.Context, harnesses []string, logf func(format string, args ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var errs []error
	for _, h := range harnesses {
		if err := Install(ctx, h, logf); err != nil {
			errs = append(errs, err)
			continue
		}
		logf("install %s: done", h)
	}
	return errors.Join(errs...)
}

func verify(ctx context.Context, binary string, logf func(string, ...any)) {
	// Prefer the freshly installed copy: PATH may still resolve to an
	// older system binary.
	var path string
	if dir, err := binDir(); err == nil {
		if _, serr := os.Stat(filepath.Join(dir, binary)); serr == nil {
			path = filepath.Join(dir, binary)
		}
	}
	if path == "" {
		var err error
		if path, err = exec.LookPath(binary); err != nil {
			return
		}
	}
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, path, "--version").CombinedOutput()
	if err != nil {
		logf("verify %s --version: %v", binary, err)
		return
	}
	if len(out) > 0 {
		logf("%s", firstLine(out))
	}
}

func firstLine(b []byte) string {
	if i := slices.Index(b, byte('\n')); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func warnPath(logf func(string, ...any)) {
	dir, err := binDir()
	if err != nil {
		return
	}
	if !slices.Contains(filepath.SplitList(os.Getenv("PATH")), dir) {
		logf("note: %s is not in PATH; add it to your shell profile", dir)
	}
}

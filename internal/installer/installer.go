// Package installer installs harness CLIs through mise. hauth does not
// manage tool versions itself; it delegates to the user's global mise
// config so workspace images and manual installs stay in charge.
package installer

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

type tool struct {
	// Mise is the tool name in the mise registry.
	Mise string
	// Binary is the executable the install is expected to provide.
	Binary string
}

var tools = map[string]tool{
	"gh":       {Mise: "gh", Binary: "gh"},
	"claude":   {Mise: "claude-code", Binary: "claude"},
	"codex":    {Mise: "codex", Binary: "codex"},
	"opencode": {Mise: "opencode", Binary: "opencode"},
	"agy":      {Mise: "antigravity-cli", Binary: "agy"},
}

// Known reports whether the harness has an installer mapping.
func Known(harness string) bool {
	_, ok := tools[harness]
	return ok
}

// BinaryFor returns the executable name a harness's CLI installs as.
func BinaryFor(harness string) (string, bool) {
	t, ok := tools[harness]
	if !ok {
		return "", false
	}
	return t.Binary, true
}

// Installed reports whether the harness's CLI is on PATH. Unmapped
// harnesses report false.
func Installed(harness string) bool {
	t, ok := tools[harness]
	if !ok {
		return false
	}
	_, err := exec.LookPath(t.Binary)
	return err == nil
}

// Available reports whether mise is on PATH.
func Available() bool {
	_, err := exec.LookPath("mise")
	return err == nil
}

var errNoMise = errors.New("mise is required to install harnesses: https://mise.jdx.dev")

// Argv returns the command that installs the harness's CLI globally.
func Argv(harness string) ([]string, error) {
	t, ok := tools[harness]
	if !ok {
		return nil, fmt.Errorf("no installer for harness %q", harness)
	}
	if !Available() {
		return nil, errNoMise
	}
	// `mise use -g` installs the tool and pins it in the global config, so
	// the workspace keeps it across restarts.
	return []string{"mise", "use", "-g", t.Mise + "@latest"}, nil
}

// InstallAll installs the given harnesses sequentially, streaming combined
// output through logf. A failing harness does not stop the others; the
// joined error is returned at the end.
func InstallAll(ctx context.Context, harnesses []string, logf func(format string, args ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var errs []error
	for _, h := range harnesses {
		argv, err := Argv(h)
		if err != nil {
			logf("install %s: %v", h, err)
			errs = append(errs, fmt.Errorf("%s: %w", h, err))
			continue
		}
		logf("install %s: %v", h, argv)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		out, err := cmd.CombinedOutput()
		if len(out) > 0 {
			logf("install %s: %s", h, out)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", h, err))
			continue
		}
		logf("install %s: done", h)
	}
	return errors.Join(errs...)
}

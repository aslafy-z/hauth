// Package common holds the shared configuration and exec plumbing used by
// every harness adapter.
package common

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aslafy-z/hauth/internal/bundle"
	"github.com/aslafy-z/hauth/internal/source"
)

// Runner abstracts command execution so probes are testable without the
// real CLIs installed. err is reserved for failures to run at all (binary
// missing, context canceled); a non-zero exit is reported via exitCode.
type Runner func(ctx context.Context, name string, args ...string) (stdout, stderr string, exitCode int, err error)

// ExecRunner is the default Runner backed by os/exec.
func ExecRunner(ctx context.Context, name string, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
			err = nil
		} else {
			code = -1
		}
	}
	return out.String(), errb.String(), code, err
}

// Config is passed to every adapter constructor.
type Config struct {
	Home     string
	StateDir string
	Runner   Runner
}

// Complete fills zero fields with defaults.
func (c Config) Complete() Config {
	if c.Home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			c.Home = h
		}
	}
	if c.StateDir == "" {
		c.StateDir = source.StateDir()
	}
	if c.Runner == nil {
		c.Runner = ExecRunner
	}
	return c
}

// Run invokes the configured Runner.
func (c Config) Run(ctx context.Context, name string, args ...string) (string, string, int, error) {
	return c.Runner(ctx, name, args...)
}

// Abs resolves a slash-separated home-relative path.
func (c Config) Abs(rel string) string {
	return filepath.Join(c.Home, filepath.FromSlash(rel))
}

// RemoveFiles deletes every existing file matching the adapter's patterns.
// Only files under home that match are touched.
func RemoveFiles(home string, patterns []string) error {
	rels, err := bundle.ExpandGlobs(home, patterns)
	if err != nil {
		return err
	}
	var errs []error
	for _, rel := range rels {
		abs := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// EnvManagedError explains why logout is impossible for env-sourced auth:
// hauth cannot unset another process's environment.
func EnvManagedError(vars []string) error {
	return fmt.Errorf("credentials are set by the environment (%s); remove the variable from your workspace configuration instead", strings.Join(vars, ", "))
}

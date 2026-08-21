package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/spf13/cobra"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/session"
)

func newLoginCmd(f *rootFlags) *cobra.Command {
	var (
		method  string
		noStore bool
	)
	cmd := &cobra.Command{
		Use:   "login <harness>",
		Short: "Run a browser-free login flow from the terminal",
		Long: "Runs the harness's login flow in a pty, printing detected URLs and\n" +
			"one-time codes to stdout. Lines read from stdin are forwarded to the\n" +
			"flow (paste codes or tokens there). On success the credentials are\n" +
			"pushed to the store unless --no-store is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := setup(f, nil)
			if err != nil {
				return err
			}
			harness := args[0]
			ad, ok := adapter.Get(harness)
			if !ok {
				return fmt.Errorf("unknown harness %q (known: %s)", harness, knownHarnesses())
			}
			m, err := pickMethod(ad, method)
			if err != nil {
				return err
			}
			return runTerminalLogin(cmd.Context(), e, harness, m, noStore, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&method, "method", "", "login method id (default: the harness's first method)")
	cmd.Flags().BoolVar(&noStore, "no-store", false, "do not push the result to the store")
	return cmd
}

func knownHarnesses() string {
	names := ""
	for i, ad := range adapter.All() {
		if i > 0 {
			names += ", "
		}
		names += ad.Name()
	}
	return names
}

func pickMethod(ad adapter.Adapter, id string) (adapter.Method, error) {
	methods := ad.Methods()
	if len(methods) == 0 {
		return adapter.Method{}, fmt.Errorf("harness %q has no login methods", ad.Name())
	}
	if id == "" {
		return methods[0], nil
	}
	for _, m := range methods {
		if m.ID == id {
			return m, nil
		}
	}
	ids := ""
	for i, m := range methods {
		if i > 0 {
			ids += ", "
		}
		ids += m.ID
	}
	return adapter.Method{}, fmt.Errorf("harness %q has no method %q (available: %s)", ad.Name(), id, ids)
}

// lockedWriter serializes writes: session output and detection events reach
// the terminal from different goroutines.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func runTerminalLogin(ctx context.Context, e *env, harness string, m adapter.Method, noStore bool, in io.Reader, rawOut io.Writer) error {
	out := &lockedWriter{w: rawOut}
	opts := session.Options{
		Home:     e.home,
		StateDir: e.stateDir,
		OnEvent: func(ev adapter.Event) {
			switch ev.Kind {
			case adapter.EventURL:
				fmt.Fprintf(out, "\n==> Open: %s\n", ev.URL)
			case adapter.EventCode:
				fmt.Fprintf(out, "\n==> Code: %s\n", ev.Code)
			case adapter.EventPrompt:
				fmt.Fprintf(out, "\n==> Waiting for input (type it here and press Enter)\n")
			}
		},
		OnOutput: func(p []byte) { _, _ = out.Write(p) },
	}
	s, err := session.Start(ctx, harness, m, opts)
	if err != nil {
		return err
	}
	go func() {
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			if err := s.SendLine(scanner.Text()); err != nil {
				return
			}
		}
	}()
	<-s.Done()
	state, code := s.State()
	switch state {
	case session.StateSucceeded:
		fmt.Fprintln(out, "\nlogin succeeded")
	case session.StateCanceled:
		return fmt.Errorf("login canceled")
	default:
		return fmt.Errorf("login failed (exit %d)", code)
	}
	if e.syncer != nil && !noStore {
		if err := e.syncer.Adopt(context.WithoutCancel(ctx), harness); err != nil {
			return fmt.Errorf("login succeeded but store push failed: %w", err)
		}
		fmt.Fprintln(out, "credentials pushed to the store")
	}
	return nil
}

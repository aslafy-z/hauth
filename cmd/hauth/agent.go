package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/app"
	"github.com/aslafy-z/hauth/internal/installer"
	"github.com/aslafy-z/hauth/internal/kiosk"
)

func newAgentCmd(f *rootFlags) *cobra.Command {
	var (
		listen  string
		noUI    bool
		origins string
		install string
	)
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Restore credentials, watch for changes, serve the kiosk",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logf := log.New(os.Stderr, "", log.LstdFlags).Printf
			e, err := setup(f, logf)
			if err != nil {
				return err
			}
			a := app.New(app.Config{
				User:     e.user,
				Instance: e.instance,
				Home:     e.home,
				StateDir: e.stateDir,
				Syncer:   e.syncer,
				Logf:     logf,
			})
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if install != "" {
				// Installation must not delay the kiosk: logins for
				// already-present CLIs work while the rest download.
				go installHarnesses(ctx, install, logf)
			}
			return a.RunAgent(ctx, listen, splitOrigins(origins), noUI)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", envOr("HAUTH_LISTEN", kiosk.DefaultListen), "kiosk listen address (loopback only)")
	cmd.Flags().BoolVar(&noUI, "no-ui", false, "run the sync loop without the kiosk")
	cmd.Flags().StringVar(&origins, "allowed-origins", os.Getenv("HAUTH_ALLOWED_ORIGINS"), "extra allowed origins, comma separated")
	cmd.Flags().StringVar(&install, "install", os.Getenv("HAUTH_INSTALL"), "harness CLIs to install at start, comma separated, or \"all\"")
	return cmd
}

// installHarnesses resolves the --install flag value and installs the
// missing CLIs in the background.
func installHarnesses(ctx context.Context, spec string, logf func(string, ...any)) {
	var names []string
	if spec == "all" {
		for _, ad := range adapter.All() {
			if installer.Known(ad.Name()) && !installer.Installed(ad.Name()) {
				names = append(names, ad.Name())
			}
		}
	} else {
		for _, name := range splitOrigins(spec) { // same csv splitting rules
			if !installer.Known(name) {
				logf("install: no installer for harness %q, skipping", name)
				continue
			}
			if installer.Installed(name) {
				logf("install: %s already installed", name)
				continue
			}
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		logf("install: nothing to do")
		return
	}
	if err := installer.InstallAll(ctx, names, logf); err != nil {
		logf("install: %v", err)
	}
}

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/aslafy-z/hauth/internal/app"
	"github.com/aslafy-z/hauth/internal/kiosk"
)

func newAgentCmd(f *rootFlags) *cobra.Command {
	var (
		listen  string
		noUI    bool
		origins string
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
			return a.RunAgent(ctx, listen, splitOrigins(origins), noUI)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", envOr("HAUTH_LISTEN", kiosk.DefaultListen), "kiosk listen address (loopback only)")
	cmd.Flags().BoolVar(&noUI, "no-ui", false, "run the sync loop without the kiosk")
	cmd.Flags().StringVar(&origins, "allowed-origins", os.Getenv("HAUTH_ALLOWED_ORIGINS"), "extra allowed origins, comma separated")
	return cmd
}

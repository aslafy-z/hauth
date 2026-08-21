package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/installer"
)

func newInstallCmd(f *rootFlags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install [harness...]",
		Short: "Install harness CLIs through mise",
		Long: "Installs the given harness CLIs (or every missing one when no\n" +
			"argument is given) using `mise use -g`, so the tools are pinned in\n" +
			"the global mise config and survive workspace restarts.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := setup(f, nil); err != nil {
				return err
			}
			names := args
			if len(names) == 0 {
				for _, ad := range adapter.All() {
					name := ad.Name()
					if installer.Known(name) && (force || !installer.Installed(name)) {
						names = append(names, name)
					}
				}
			} else {
				kept := names[:0]
				for _, name := range names {
					if !installer.Known(name) {
						return fmt.Errorf("no installer for harness %q", name)
					}
					if !force && installer.Installed(name) {
						fmt.Fprintf(cmd.OutOrStdout(), "%s already installed\n", name)
						continue
					}
					kept = append(kept, name)
				}
				names = kept
			}
			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing to install")
				return nil
			}
			logf := func(format string, args ...any) {
				fmt.Fprintf(cmd.OutOrStdout(), format+"\n", args...)
			}
			return installer.InstallAll(cmd.Context(), names, logf)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "reinstall even when the CLI is already present")
	return cmd
}

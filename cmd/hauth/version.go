package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aslafy-z/hauth/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("hauth %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
		},
	}
}

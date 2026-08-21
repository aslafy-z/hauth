package main

import (
	"errors"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/store"
)

func newStoreCmd(f *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store",
		Short: "Inspect and manage store entries",
	}
	cmd.AddCommand(
		newStoreListCmd(f),
		newStoreRestoreCmd(f),
		newStorePushCmd(f),
		newStoreAdoptCmd(f),
		newStoreDeleteCmd(f),
	)
	return cmd
}

func storeEnv(f *rootFlags) (*env, error) {
	e, err := setup(f, nil)
	if err != nil {
		return nil, err
	}
	if e.store == nil {
		return nil, errors.New("no store configured (--store none)")
	}
	return e, nil
}

func newStoreListCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List stored harness bundles and leases",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := storeEnv(f)
			if err != nil {
				return err
			}
			entries, err := e.store.List(cmd.Context(), e.user)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "HARNESS\tREV\tUPDATED\tBY\tLEASE")
			for _, en := range entries {
				lease := ""
				if en.Lease.Live(time.Now()) {
					lease = fmt.Sprintf("%s (until %s)", en.Lease.Holder, en.Lease.ExpiresAt.Local().Format(time.TimeOnly))
				}
				updated := ""
				if !en.UpdatedAt.IsZero() {
					updated = en.UpdatedAt.Local().Format(time.DateTime)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", en.Harness, en.Rev, updated, en.Instance, lease)
			}
			return w.Flush()
		},
	}
}

func newStoreRestoreCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "restore",
		Short: "Restore stored credentials into this home directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := storeEnv(f)
			if err != nil {
				return err
			}
			return e.syncer.RestoreAll(cmd.Context())
		},
	}
}

func newStorePushCmd(f *rootFlags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "push [harness]",
		Short: "Push local credentials to the store",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := storeEnv(f)
			if err != nil {
				return err
			}
			names := args
			if len(names) == 0 {
				for _, ad := range adapter.All() {
					names = append(names, ad.Name())
				}
			}
			for _, name := range names {
				pushed, err := e.syncer.PushHarness(cmd.Context(), name, force)
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				if pushed {
					fmt.Println("pushed", name)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "push even when the lease or source rules would skip it")
	return cmd
}

func newStoreAdoptCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "adopt <harness>",
		Short: "Adopt externally created credential files into the store",
		Long: "Pushes credential files that hauth did not create into the store and\n" +
			"marks them as store-managed on this machine. Adoption is always an\n" +
			"explicit action so a personal token never syncs unnoticed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := storeEnv(f)
			if err != nil {
				return err
			}
			if err := e.syncer.Adopt(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Println("adopted", args[0])
			return nil
		},
	}
}

func newStoreDeleteCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <harness>",
		Short: "Delete a harness's bundle and lease from the store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := storeEnv(f)
			if err != nil {
				return err
			}
			if err := e.syncer.DeleteHarness(cmd.Context(), args[0]); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("nothing stored for %s", args[0])
				}
				return err
			}
			fmt.Println("deleted", args[0])
			return nil
		},
	}
}

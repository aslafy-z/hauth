package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aslafy-z/hauth/internal/adapter"
)

func newStatusCmd(f *rootFlags) *cobra.Command {
	var asJSON bool
	var check bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the auth source and validity of every harness",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := setup(f, nil); err != nil {
				return err
			}
			type row struct {
				Harness   string    `json:"harness"`
				Source    string    `json:"source"`
				Validity  string    `json:"validity"`
				Account   string    `json:"account,omitempty"`
				Detail    string    `json:"detail"`
				Refresh   string    `json:"refresh"`
				CheckedAt time.Time `json:"checked_at"`
			}
			var rows []row
			for _, ad := range adapter.All() {
				st, err := ad.Probe(cmd.Context(), !check)
				if err != nil {
					st.Detail = "probe failed: " + err.Error()
				}
				rows = append(rows, row{
					Harness:   ad.Name(),
					Source:    st.Source.String(),
					Validity:  st.Valid.String(),
					Account:   st.Account,
					Detail:    st.Detail,
					Refresh:   ad.Refresh().String(),
					CheckedAt: st.CheckedAt,
				})
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "HARNESS\tSOURCE\tVALIDITY\tACCOUNT\tDETAIL")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Harness, r.Source, r.Validity, r.Account, r.Detail)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().BoolVar(&check, "check", false, "run full probes (may make network calls)")
	return cmd
}

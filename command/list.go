package command

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newList(composition Options) *cobra.Command {
	var selection catalogFlags
	var asJSON bool
	command := &cobra.Command{
		Use: "list", Short: "List validated cases, profiles and tags without Docker",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			catalog, err := selection.load(command.Context(), composition)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(command.OutOrStdout()).Encode(catalog.Manifest())
			}
			out := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(out, "ID\tPROFILE\tTAGS\tDESCRIPTION"); err != nil {
				return err
			}
			for _, document := range catalog.Scenarios() {
				definition := document.Snapshot()
				if _, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\n",
					document.ID(), definition.Environment.Profile, strings.Join(definition.Tags, ","), definition.Description); err != nil {
					return err
				}
			}
			return out.Flush()
		},
	}
	selection.bind(command)
	command.Flags().BoolVar(&asJSON, "json", false, "write the offline manifest to stdout")
	return command
}

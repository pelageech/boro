// Package command builds a runner from explicitly supplied capabilities.
package command

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"boro/scenario"
)

// New constructs the command surface for the caller's compiled composition.
func New(options Options) *cobra.Command {
	options.Profiles = scenario.Clone(options.Profiles)
	root := &cobra.Command{Use: "boro", SilenceUsage: true, SilenceErrors: true}
	var selection catalogFlags
	var report string
	validate := &cobra.Command{Use: "validate", Short: "Validate a catalog offline and write its selected manifest", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if report == "" {
			return fmt.Errorf("--report is required: %w", scenario.ErrInput)
		}
		catalog, err := selection.load(command.Context(), options)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(catalog.Manifest(), "", "  ")
		if err != nil {
			return fmt.Errorf("encode manifest: %w", err)
		}
		if err := command.Context().Err(); err != nil {
			return fmt.Errorf("validation canceled before report: %w", err)
		}
		if err := os.WriteFile(report, append(data, '\n'), 0o600); err != nil {
			return fmt.Errorf("write validation report: %w", err)
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "Validated %d scenarios; manifest: %s\n", len(catalog.Scenarios()), report)
		return err
	}}
	validate.Flags().StringVar(&report, "report", "", "selected manifest output file")
	selection.bind(validate)
	root.AddCommand(validate, newList(options), newDescribe(options), newRun(options), newReset(options), newContract(options))
	if options.Capture != nil {
		root.AddCommand(newCapture(options))
	}
	if options.Probe != nil {
		root.AddCommand(newBindings(options.Probe))
	}
	return root
}

package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/pelageech/boro"
	"github.com/pelageech/boro/scenario"
)

func newBindings(probe boro.BindingProbe) *cobra.Command {
	var environment, output string
	command := &cobra.Command{
		Use: "bindings", Short: "Observe actual protocol and socket identities inside the runner", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) (err error) {
			if environment == "" || output == "" {
				return fmt.Errorf("--environment and --output are required: %w", scenario.ErrInput)
			}
			manifest, err := readResetInput(environment, boro.ReadEnvironment)
			if err != nil {
				return err
			}
			file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: the CLI caller selects the output path; O_EXCL prevents overwrites.
			if err != nil {
				return fmt.Errorf("create fresh binding evidence: %w", err)
			}
			defer func() { err = errors.Join(err, file.Close()) }()
			report, probeErr := boro.ProbeBindings(command.Context(), manifest, probe)
			err = json.NewEncoder(file).Encode(report)
			if probeErr != nil {
				return errors.Join(probeErr, err)
			}
			_, printErr := fmt.Fprintf(command.OutOrStdout(), "Observed %d concrete bindings at %s\n",
				len(report.Observations), report.FinishedAt.Format(time.RFC3339Nano))
			return errors.Join(err, printErr)
		},
	}
	command.Flags().StringVar(&environment, "environment", "", "concrete manifest to observe")
	command.Flags().StringVar(&output, "output", "", "fresh binding evidence path")
	return command
}

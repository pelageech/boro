package command

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"boro/scenario"
)

func newCapture(composition Options) *cobra.Command {
	options := &runFlags{composition: composition.Composition, profiles: composition.Profiles}
	var id string
	var timeout time.Duration
	command := &cobra.Command{
		Use: "capture", Short: "Read the initial owned environment generation into an immutable reset baseline",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) (err error) {
			if options.scenario == "" || options.environment == "" || options.invocation == "" || options.result == "" || id == "" {
				return fmt.Errorf("capture requires --scenario, --environment, --invocation, --output and --id: %w", scenario.ErrInput)
			}
			resolved, err := options.resolve(command)
			if err != nil {
				return err
			}
			output, err := os.OpenFile(options.result, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return fmt.Errorf("create fresh baseline: %w", err)
			}
			defer func() { err = errors.Join(err, output.Close()) }()
			data, err := composition.Capture(command.Context(), resolved, id, timeout, options.cleanupTimeout)
			if err != nil {
				return err
			}
			if _, err := output.Write(data); err != nil {
				return fmt.Errorf("write captured baseline: %w", err)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Captured baseline %s for %s; no scenario assertions ran\n", id, resolved.Environment().Project)
			return err
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.scenario, "scenario", "", "first scenario YAML")
	flags.StringVar(&options.environment, "environment", "", "initial owned environment JSON")
	flags.StringVar(&options.invocation, "invocation", "", "expected first case invocation")
	flags.StringVar(&options.fixtures, "fixtures", "", "immutable scenario fixture root")
	flags.IntVar(&options.parallelActions, "parallel-actions", 2, "declared scenario branch limit")
	flags.StringVar(&options.result, "output", "", "fresh captured baseline JSON path")
	flags.StringVar(&id, "id", "", "baseline identity")
	flags.DurationVar(&timeout, "timeout", 3*time.Minute, "total capture observation bound")
	flags.DurationVar(&options.cleanupTimeout, "cleanup-timeout", 10*time.Second, "independent capture client release bound")
	return command
}

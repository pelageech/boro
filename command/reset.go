package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/pelageech/boro"
	"github.com/pelageech/boro/scenario"
)

type resetFlags struct {
	runFlags

	baseline  string
	context   string
	execution string
	timeout   time.Duration
}

func newReset(composition Options) *cobra.Command {
	options := &resetFlags{runFlags: runFlags{composition: composition.Composition, profiles: composition.Profiles}}
	command := &cobra.Command{
		Use: "reset", Short: "Restore and check an owned case baseline without finalizing the case",
		Args: cobra.NoArgs, RunE: options.run,
	}
	flags := command.Flags()
	flags.StringVar(&options.scenario, "scenario", "", "the executed scenario YAML")
	flags.StringVar(&options.environment, "environment", "", "the exact case environment JSON")
	flags.StringVar(&options.invocation, "invocation", "", "expected case invocation")
	flags.StringVar(&options.fixtures, "fixtures", "", "immutable scenario fixture root")
	flags.IntVar(&options.parallelActions, "parallel-actions", 2, "declared scenario branch limit")
	flags.StringVar(&options.baseline, "baseline", "", "immutable strict ResetBaseline JSON")
	flags.StringVar(&options.context, "context", "", "owner-recorded ResetContext JSON fixed before dispatch")
	flags.StringVar(&options.execution, "execution", "", "successful full native Ginkgo JSON report")
	flags.StringVar(&options.result, "receipt", "", "fresh ResetReceipt JSON path, with .events.jsonl acquisition log")
	flags.DurationVar(&options.timeout, "timeout", 30*time.Second, "total reset restoration/observation bound")
	flags.DurationVar(&options.cleanupTimeout, "cleanup-timeout", 10*time.Second, "independent reset-local release bound")
	return command
}

func (f *resetFlags) run(command *cobra.Command, _ []string) error {
	if f.scenario == "" || f.environment == "" || f.invocation == "" || f.baseline == "" ||
		f.context == "" || f.execution == "" || f.result == "" || f.timeout <= 0 || f.cleanupTimeout <= 0 {
		return fmt.Errorf("reset requires --scenario, --environment, --invocation, --baseline, --context, --execution, --receipt and positive bounds: %w",
			scenario.ErrInput)
	}
	resolved, err := f.resolve(command)
	if err != nil {
		return err
	}
	baseline, err := readResetInput(f.baseline, f.composition.Schema().ReadResetBaseline)
	if err != nil {
		return err
	}
	expected, err := readResetInput(f.context, boro.ReadResetContext)
	if err != nil {
		return err
	}
	execution, err := readResetInput(f.execution, boro.ReadSuiteReport)
	if err != nil {
		return err
	}
	if err := boro.PreflightReset(resolved, baseline, expected, execution); err != nil {
		return err
	}
	return f.executeReset(command, resolved, boro.ResetOptions{
		Baseline: baseline, Context: expected, Execution: execution, Timeout: f.timeout, CleanupTimeout: f.cleanupTimeout,
	})
}

func readResetInput[T any](path string, decode func(io.Reader) (T, error)) (T, error) {
	var zero T
	file, err := os.Open(path) //nolint:gosec // G304: reset input paths are explicitly selected by the CLI caller.
	if err != nil {
		return zero, fmt.Errorf("open reset input %s: %w", path, err)
	}
	value, decodeErr := decode(file)
	return value, errors.Join(decodeErr, file.Close())
}

func (f *resetFlags) executeReset(command *cobra.Command, resolved boro.ResolvedScenario, options boro.ResetOptions) (err error) {
	output, err := os.OpenFile(f.result, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create fresh reset receipt: %w", err)
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	events, err := os.OpenFile(f.result+".events.jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create fresh reset acquisition log: %w", err)
	}
	defer func() { err = errors.Join(err, events.Close()) }()
	options.Events = events
	receipt, resetErr := boro.Reset(command.Context(), resolved, options)
	if err := json.NewEncoder(output).Encode(receipt); err != nil {
		return errors.Join(resetErr, fmt.Errorf("write reset receipt: %w", err))
	}
	if resetErr != nil {
		return resetErr
	}
	_, err = fmt.Fprintf(command.OutOrStdout(),
		"Reset observed %d baseline checks for %s; no scenario assertion credit; owner reconciliation required; receipt: %s\n",
		len(receipt.Observations), resolved.Identity().ScenarioID, f.result)
	return err
}

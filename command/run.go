package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/onsi/ginkgo/v2/types"
	"github.com/spf13/cobra"

	"github.com/pelageech/boro"
	"github.com/pelageech/boro/scenario"
	"github.com/pelageech/boro/suite"
)

type runFlags struct {
	composition     boro.Composition
	profiles        []scenario.ProfileDeclaration
	scenario        string
	environment     string
	invocation      string
	mode            string
	result          string
	fixtures        string
	parallelActions int
	cleanupTimeout  time.Duration
}

func newRun(composition Options) *cobra.Command {
	options := &runFlags{composition: composition.Composition, profiles: composition.Profiles}
	command := &cobra.Command{
		Use: "run", Short: "Execute one YAML scenario as native Ginkgo specs",
		Args: cobra.NoArgs, RunE: options.run,
	}
	flags := command.Flags()
	flags.StringVar(&options.scenario, "scenario", "", "one scenario YAML file")
	flags.StringVar(&options.environment, "environment", "", "resolved environment JSON file")
	flags.StringVar(&options.invocation, "invocation", "", "expected invocation identity")
	flags.StringVar(&options.mode, "mode", "full", "full or configure")
	flags.StringVar(&options.result, "result", "", "new native Ginkgo JSON report (never overwrites)")
	flags.StringVar(&options.fixtures, "fixtures", "", "read-only fixture root")
	flags.IntVar(&options.parallelActions, "parallel-actions", 2, "maximum explicit branches")
	flags.DurationVar(&options.cleanupTimeout, "cleanup-timeout", 30*time.Second, "independent temporary-resource cleanup bound")
	return command
}

func (f *runFlags) run(command *cobra.Command, _ []string) error {
	if f.scenario == "" || f.environment == "" || f.invocation == "" || f.result == "" {
		return fmt.Errorf("--scenario, --environment, --invocation and --result are required: %w", scenario.ErrInput)
	}
	if f.cleanupTimeout <= 0 {
		return fmt.Errorf("--cleanup-timeout must be positive: %w", scenario.ErrInput)
	}
	switch boro.Mode(f.mode) {
	case boro.ModeFull, boro.ModeConfigure:
	default:
		return fmt.Errorf("--mode must be full or configure: %w", scenario.ErrInput)
	}
	resolved, err := f.resolve(command)
	if err != nil {
		return err
	}
	return f.execute(command, resolved)
}

func (f *runFlags) resolve(command *cobra.Command) (boro.ResolvedScenario, error) {
	registry := f.composition
	loaded, err := scenario.LoadScenario(command.Context(), f.scenario, scenario.LoadOptions{
		FixtureRoot: f.fixtures, ParallelActions: f.parallelActions, Schema: registry.Schema(), Profiles: f.profiles,
	})
	if err != nil {
		return boro.ResolvedScenario{}, err
	}
	environmentFile, err := os.Open(f.environment)
	if err != nil {
		return boro.ResolvedScenario{}, fmt.Errorf("open environment: %w", err)
	}
	environment, parseErr := boro.ReadEnvironment(environmentFile)
	if err := errors.Join(parseErr, environmentFile.Close()); err != nil {
		return boro.ResolvedScenario{}, err
	}
	if f.invocation != environment.InvocationID {
		return boro.ResolvedScenario{}, fmt.Errorf("invocation does not match environment: %w", scenario.ErrInput)
	}
	return boro.Resolve(command.Context(), loaded, environment, registry)
}

func (f *runFlags) execute(command *cobra.Command, resolved boro.ResolvedScenario) (err error) {
	output, err := os.OpenFile(f.result, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create fresh result: %w", err)
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	report, runErr := suite.Run(command.Context(), resolved, suite.Options{
		Mode: boro.Mode(f.mode), CleanupTimeout: f.cleanupTimeout,
	})
	if err := json.NewEncoder(output).Encode([]types.Report{report}); err != nil {
		return errors.Join(runErr, fmt.Errorf("write native Ginkgo report: %w", err))
	}
	if runErr != nil {
		return runErr
	}
	if boro.Mode(f.mode) == boro.ModeConfigure {
		_, err = fmt.Fprintf(command.OutOrStdout(), "Configured %s; no checks ran; Ginkgo report: %s\n", resolved.Identity().ScenarioID, f.result)
	} else {
		_, err = fmt.Fprintf(command.OutOrStdout(),
			"Executed YAML scenario %s; Ginkgo report: %s\n", resolved.Identity().ScenarioID, f.result)
	}
	return err
}

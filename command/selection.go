package command

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pelageech/boro/scenario"
)

type catalogFlags struct {
	catalogs, cases, tags []string
	fixtures              string
	parallelActions       int
}

func (f *catalogFlags) bind(command *cobra.Command) {
	f.bindSelection(command)
	command.Flags().StringVar(&f.fixtures, "fixtures", "", "fixture root (default: sibling fixtures directory)")
	command.Flags().IntVar(&f.parallelActions, "parallel-actions", 2, "maximum explicit branches")
}

func (f *catalogFlags) bindSelection(command *cobra.Command) {
	flags := command.Flags()
	flags.StringArrayVar(&f.catalogs, "catalog", nil, "catalog directory (required; repeatable)")
	flags.StringArrayVar(&f.cases, "case", nil, "exact case ID (repeatable)")
	flags.StringArrayVar(&f.tags, "tag", nil, "exact tag (repeatable; OR within tags, AND with cases)")
}

func (f catalogFlags) load(ctx context.Context, options Options) (scenario.Catalog, error) {
	if len(f.catalogs) == 0 {
		return scenario.Catalog{}, fmt.Errorf("at least one --catalog is required: %w", scenario.ErrInput)
	}
	if f.parallelActions < 2 {
		return scenario.Catalog{}, fmt.Errorf("--parallel-actions must be at least two: %w", scenario.ErrInput)
	}
	return scenario.LoadCatalog(ctx, f.catalogs, scenario.LoadOptions{FixtureRoot: f.fixtures, ParallelActions: f.parallelActions, Schema: options.Composition.Schema(), Profiles: options.Profiles, Selection: scenario.Selection{Cases: f.cases, Tags: f.tags}})
}

package boro

import (
	"context"
	"errors"
	"fmt"
	"time"

	scen "github.com/pelageech/boro/scenario"
)

// PrepareInvocation prepares the selected YAML tree without executing mutations.
// The returned scope must be closed even when preparation fails. All operations
// share one materialization of the registry's invocation-local factories.
func (r ResolvedScenario) PrepareInvocation(ctx context.Context, mode Mode) (*RunScope, map[string]PreparedOperation, error) {
	if err := r.RequireExecution(mode); err != nil {
		return nil, nil, err
	}
	scope := r.NewRunScope()
	prepared, err := prepareOperations(ctx, scope, r.registry.forInvocation(), r.Snapshot(), mode)
	return scope, prepared, err
}

// RequireExecution checks the selected mode and handlers without acquiring clients.
func (r ResolvedScenario) RequireExecution(mode Mode) error {
	if r.hash == "" || r.environment.InvocationID == "" {
		return inputError("invocation", "a resolved invocation is required")
	}
	switch mode {
	case ModeFull, ModeConfigure:
	default:
		return inputError("mode", "expected full or configure")
	}
	if r.environment.Ownership == environmentAttached && !r.Snapshot().Environment.AllowAttach {
		return inputError("environment", "scenario does not permit attachment")
	}
	if mode == ModeConfigure {
		return r.registry.require(r.Snapshot().Setup)
	}
	return r.registry.RequireExecution(r.scenario)
}

func prepareOperations(
	ctx context.Context, scope *RunScope, registry Bindings, definition scen.Definition, mode Mode,
) (map[string]PreparedOperation, error) {
	prepared := make(map[string]PreparedOperation)
	var visit func([]scen.Step) error
	visit = func(steps []scen.Step) error {
		for _, step := range steps {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("preparation interrupted: %w", err)
			}
			for _, branch := range step.Branches {
				if err := visit(branch.Steps); err != nil {
					return err
				}
			}
			if step.Operation == "" {
				continue
			}
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(definition.Timeouts.Action))
			operation, err := registry.Prepare(callCtx, scope, step)
			err = errors.Join(err, callCtx.Err())
			cancel()
			if err != nil {
				return fmt.Errorf("prepare step %s: %w", step.ID, err)
			}
			prepared[step.ID] = operation
		}
		return nil
	}
	groups := [][]scen.Step{definition.Setup}
	if mode == ModeFull {
		groups = append(groups, definition.Steps, definition.Teardown)
	}
	for _, steps := range groups {
		if err := visit(steps); err != nil {
			return prepared, err
		}
	}
	return prepared, nil
}

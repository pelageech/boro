package suite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"boro"
	scen "boro/scenario"
)

type stepExecution struct {
	ctx        context.Context
	definition scen.Definition
	prepared   map[string]boro.PreparedOperation
	record     func(string, any)
}

func (e *stepExecution) step(ctx context.Context, step scen.Step) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("step %s canceled: %w", step.ID, err)
	}
	if step.Branches != nil {
		return "", e.parallel(ctx, step)
	}
	if step.Want != nil {
		return e.observe(ctx, step)
	}
	timeout := time.Duration(e.definition.Timeouts.Action)
	if step.Timeout != nil {
		timeout = time.Duration(*step.Timeout)
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	result, err := e.prepared[step.ID].Action(callCtx)
	err = errors.Join(err, callCtx.Err())
	e.record("boro.action", ActionCompletion{
		StepID: step.ID, StartedAt: startedAt, FinishedAt: time.Now(), Detail: result.Detail, Error: errorText(err),
	})
	return result.Detail, err
}

// YAML branches share prepared state and start together. They are not separate
// Ginkgo specs: sibling failure must cancel and join every branch before return.
func (e *stepExecution) parallel(ctx context.Context, step scen.Step) error {
	blockCtx, cancel := context.WithCancel(ctx)
	if step.Timeout != nil {
		cancel()
		blockCtx, cancel = context.WithTimeout(ctx, time.Duration(*step.Timeout))
	}
	defer cancel()
	group, branchCtx := errgroup.WithContext(blockCtx)
	start := make(chan struct{})
	ready := make(chan struct{}, len(step.Branches))
	failures := make([]error, len(step.Branches))
	for i, branch := range step.Branches {
		group.Go(func() error {
			ready <- struct{}{}
			<-start
			startedAt := time.Now()
			defer func() {
				e.record("boro.branch", BranchCompletion{
					StepID: step.ID, BranchID: branch.ID, StartedAt: startedAt,
					FinishedAt: time.Now(), Error: errorText(failures[i]),
				})
			}()
			for _, child := range branch.Steps {
				if _, err := e.step(branchCtx, child); err != nil {
					failures[i] = fmt.Errorf("branch %s step %s: %w", branch.ID, child.ID, err)
					return failures[i]
				}
			}
			return nil
		})
	}
	for range step.Branches {
		<-ready
	}
	close(start)
	first := group.Wait()
	return errors.Join(first, errors.Join(failures...), blockCtx.Err())
}

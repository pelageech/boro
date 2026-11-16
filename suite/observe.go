package suite

import (
	"context"
	"errors"
	"fmt"
	"time"

	runresult "github.com/pelageech/boro/result"

	"github.com/onsi/gomega"

	scen "github.com/pelageech/boro/scenario"
)

func (e *stepExecution) observe(ctx context.Context, step scen.Step) (string, error) {
	wait := *step.Wait
	poll := time.Duration(e.definition.Timeouts.Poll)
	if wait.Poll != nil {
		poll = time.Duration(*wait.Poll)
	}
	var diagnostic, detail string
	var observationErr error
	sampleCtx := ctx
	var remainsUntil time.Time
	sampled := false
	if wait.Mode == "eventually" {
		var cancel context.CancelFunc
		sampleCtx, cancel = context.WithTimeout(ctx, time.Duration(*wait.Timeout))
		defer cancel()
	}
	if wait.Mode == "remains" {
		remainsUntil = time.Now().Add(time.Duration(*wait.Duration))
	}
	sample := func() (bool, error) {
		observationErr = observationContextError(sampleCtx)
		// A poll timer and the context deadline can become ready together.
		// Check the actual deadline before dispatch, even if the context's
		// cancellation callback has not run yet.
		if observationErr != nil {
			return false, gomega.StopTrying("observation canceled before dispatch").Wrap(observationErr)
		}
		// Join a call already started before the remains boundary, but do not
		// start another real observation after that boundary.
		if sampled && !remainsUntil.IsZero() && !time.Now().Before(remainsUntil) {
			return true, nil
		}
		callCtx, cancel := context.WithTimeout(sampleCtx, time.Duration(e.definition.Timeouts.Observation))
		startedAt := time.Now()
		sampled = true
		observed, err := e.prepared[step.ID].Observe(callCtx)
		observationErr = errors.Join(err, callCtx.Err())
		cancel()
		detail = observed.Detail
		e.record("boro.observation", ObservationSample{
			StepID: step.ID, StartedAt: startedAt, FinishedAt: time.Now(),
			Observation: observed, Error: errorText(observationErr),
		})
		if observationErr != nil {
			return false, gomega.StopTrying("observation failed").Wrap(observationErr)
		}
		return observed.Matches, nil
	}
	g := gomega.NewGomega(func(message string, _ ...int) { diagnostic = message })
	var matched bool
	switch wait.Mode {
	case "once":
		value, err := sample()
		if err != nil {
			return detail, observationErr
		}
		matched = g.Expect(value).To(gomega.BeTrue(), detail)
	case "eventually":
		matched = g.Eventually(sampleCtx, sample).WithTimeout(time.Duration(*wait.Timeout)).WithPolling(poll).
			Should(gomega.BeTrue())
	case "remains":
		matched = g.Consistently(ctx, sample).WithTimeout(time.Duration(*wait.Duration)).WithPolling(poll).
			Should(gomega.BeTrue())
	default:
		return "", fmt.Errorf("unsupported observation mode %q: %w", wait.Mode, scen.ErrInput)
	}
	if err := errors.Join(observationErr, observationContextError(sampleCtx)); err != nil {
		return detail, err
	}
	if !matched {
		return detail, fmt.Errorf("%w: %s\n%s", runresult.ErrMismatch, detail, diagnostic)
	}
	return detail, nil
}

func observationContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("observation context: %w", err)
	}
	if deadline, bounded := ctx.Deadline(); bounded && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

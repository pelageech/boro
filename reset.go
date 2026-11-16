package boro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sync"
	"time"

	runresult "github.com/pelageech/boro/result"

	"github.com/onsi/ginkgo/v2/types"

	scen "github.com/pelageech/boro/scenario"
)

// ResetOptions contains independently captured inputs and two explicit bounds.
// Reset never finalizes execution evidence or contributes scenario assertions.
type ResetOptions struct {
	Baseline       scen.ResetBaseline
	Context        ResetContext
	Execution      types.Report
	Timeout        time.Duration
	CleanupTimeout time.Duration
	Events         io.Writer
}

// ResetBoundary retains the owner's pre-dispatch expectation and consumes one
// receipt at most once. The host persists this disposition in its generation
// journal before admitting another case.
type ResetBoundary struct {
	mu        sync.Mutex
	resolved  ResolvedScenario
	baseline  scen.ResetBaseline
	expected  ResetContext
	execution types.Report
	consumed  bool
}

// NewResetBoundary records validated independent expectations before dispatch.
func NewResetBoundary(resolved ResolvedScenario, baseline scen.ResetBaseline, expected ResetContext, execution types.Report) (*ResetBoundary, error) {
	if err := PreflightReset(resolved, baseline, expected, execution); err != nil {
		return nil, err
	}
	data, err := json.Marshal([]types.Report{execution})
	if err != nil {
		return nil, fmt.Errorf("capture reset execution report: %w", err)
	}
	captured, err := ReadSuiteReport(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return &ResetBoundary{
		resolved: resolved, baseline: scen.Clone(baseline), expected: scen.Clone(expected), execution: captured,
	}, nil
}

// Consume verifies independently observed process/event evidence and rejects
// duplicate calls, including concurrent consumers. It does not finalize a case.
func (b *ResetBoundary) Consume(receipt ResetReceipt, completed ResetCompletion) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	completed.BoundaryConsumed = completed.BoundaryConsumed || b.consumed
	if err := ReconcileReset(b.resolved, b.baseline, b.expected, b.execution, receipt, completed); err != nil {
		return err
	}
	b.consumed = true
	return nil
}

type resetExecution struct {
	scope    *RunScope
	baseline scen.ResetBaseline
	receipt  ResetReceipt
	context  ResetContext
	events   io.Writer
	err      error
	schema   scen.Schema
}

// Reset restores supported typed domains, observes every baseline check, and
// joins reset-local work before returning. Failure always requires recycling.
func Reset(ctx context.Context, resolved ResolvedScenario, options ResetOptions) (ResetReceipt, error) {
	if options.Timeout <= 0 || options.CleanupTimeout <= 0 || options.Events == nil {
		return ResetReceipt{}, inputError("reset", "positive reset/cleanup bounds and an acquisition event writer are required")
	}
	if err := PreflightReset(resolved, options.Baseline, options.Context, options.Execution); err != nil {
		return ResetReceipt{}, err
	}
	baseline := scen.Clone(options.Baseline)
	expected := scen.Clone(options.Context)
	run := &resetExecution{
		scope: resolved.NewRunScope(), baseline: baseline, context: expected, events: options.Events, schema: resolved.registry.Schema(),
		receipt: ResetReceipt{
			Version: 1, Identity: expected.Identity, Generation: expected.Generation, Baseline: expected.Baseline,
			ResetID: expected.ResetID, Boundary: expected.Boundary, CaseFinishedAt: expected.CaseFinishedAt,
			StartedAt: time.Now().UTC(), Observations: []ResetObservation{}, Resources: []ResetResource{}, Errors: []ResetError{},
		},
	}
	run.scope.onOwn = run.acquire
	resetCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	prepared := make([]func(context.Context) (scen.ResetValue, error), len(baseline.ExpectedChecks))
	var operationErr error
	for i, check := range baseline.ExpectedChecks {
		prepared[i], operationErr = resolved.registry.resetHandlers[check.Domain].Prepare(resetCtx, run.scope, baseline, check)
		operationErr = errors.Join(operationErr, run.err, resetCtx.Err())
		if operationErr != nil {
			break
		}
	}
	if operationErr == nil {
		operationErr = run.restore(resetCtx, resolved.registry, baseline)
	}
	if operationErr != nil {
		run.addError("", "", failure("", operationErr).Category, operationErr)
	}
	for i, check := range baseline.ExpectedChecks {
		row := ResetObservation{
			CheckID: check.ID, Domain: check.Domain, Target: run.target(check.Target.Service),
			Outcome: scen.ResetNotRun, ErrorIDs: []string{},
		}
		if operationErr != nil {
			row.ErrorIDs = append(row.ErrorIDs, run.receipt.Errors[0].ID)
		} else {
			row = run.observe(resetCtx, check, prepared[i])
		}
		run.receipt.Observations = append(run.receipt.Observations, row)
	}
	cancel()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), options.CleanupTimeout)
	_, releaseErr := run.scope.Close(cleanupCtx, false)
	cleanupCancel()
	if releaseErr != nil && len(run.receipt.Errors) == 0 {
		run.addError("", "", "cleanup", releaseErr)
	}
	run.receipt.CompletedAt = time.Now().UTC()
	receipt := scen.Clone(run.receipt)
	for _, row := range receipt.Observations {
		if row.Outcome != scen.ResetMatched {
			operationErr = errors.Join(operationErr, fmt.Errorf("reset check %s: %w", row.CheckID, runresult.ErrMismatch))
		}
	}
	if len(receipt.Errors) > 0 {
		operationErr = errors.Join(operationErr, inputError("reset", "reset errors prevent reuse"))
	}
	return receipt, errors.Join(operationErr, releaseErr, run.err)
}

func (r *resetExecution) target(service string) scen.BoundResetTarget {
	for _, target := range r.context.BoundTargets {
		if target.Service == service {
			return target
		}
	}
	return scen.BoundResetTarget{Service: service}
}

func (r *resetExecution) addError(checkID, resourceID, category string, err error) string {
	id := fmt.Sprintf("reset-error-%d", len(r.receipt.Errors)+1)
	r.receipt.Errors = append(r.receipt.Errors, ResetError{
		ID: id, Category: category, Message: err.Error(), CheckID: checkID, ResourceID: resourceID,
	})
	return id
}

func (r *resetExecution) emit(kind string, resource ResetResource) error {
	at := resource.AcquiredAt
	if kind != "acquired" {
		at = time.Now().UTC()
		if resource.ReleasedAt != nil {
			at = *resource.ReleasedAt
		}
	}
	event := ResetEvent{
		ResetID: r.receipt.ResetID, Boundary: r.receipt.Boundary, Type: kind,
		ResourceID: resource.ID, Kind: resource.Kind, Target: resource.Target, Locator: resource.Locator, At: at,
	}
	if err := json.NewEncoder(r.events).Encode(event); err != nil {
		return fmt.Errorf("write reset acquisition/release event: %w", err)
	}
	return nil
}

func (r *resetExecution) acquire(resource TemporaryResource) TemporaryResource {
	index := len(r.receipt.Resources)
	row := ResetResource{
		ID: fmt.Sprintf("reset-resource-%d", index+1), Kind: string(resource.Identity.Kind), OwnerResetID: r.receipt.ResetID,
		Target: r.target(resource.Target), Locator: resource.Locator, AcquiredAt: time.Now().UTC(),
		State: resourceFailed, ErrorIDs: []string{},
	}
	r.receipt.Resources = append(r.receipt.Resources, row)
	if err := r.emit("acquired", row); err != nil {
		r.err = errors.Join(r.err, err)
		r.addError("", "", "operation", err)
	}
	release := resource.Release
	resource.Release = func(ctx context.Context) error {
		err := errors.Join(release(ctx), ctx.Err())
		row := &r.receipt.Resources[index]
		if err == nil {
			at := time.Now().UTC()
			row.Joined, row.State, row.ReleasedAt = true, ResourceReleased, &at
		} else {
			row.ErrorIDs = append(row.ErrorIDs, r.addError("", row.ID, "cleanup", err))
		}
		eventErr := r.emit(row.State, *row)
		if eventErr != nil {
			r.err = errors.Join(r.err, eventErr)
			r.addError("", "", "cleanup", eventErr)
		}
		return errors.Join(err, eventErr)
	}
	return resource
}

func (r *resetExecution) observe(ctx context.Context, check scen.ExpectedCheck, observe func(context.Context) (scen.ResetValue, error)) ResetObservation {
	started := time.Now().UTC()
	row := ResetObservation{
		CheckID: check.ID, Domain: check.Domain, Target: r.target(check.Target.Service),
		StartedAt: &started, ErrorIDs: []string{},
	}
	for {
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(r.scope.Scenario().Timeouts.Observation))
		actual, err := observe(callCtx)
		err = errors.Join(err, callCtx.Err())
		cancel()
		if err != nil {
			if row.Actual != nil {
				data, encodeErr := json.Marshal(row.Actual)
				if encodeErr != nil {
					err = errors.Join(err, fmt.Errorf("encode last unmatched reset inventory: %w", encodeErr))
				} else {
					err = fmt.Errorf("last unmatched inventory %s: %w", data, err)
				}
			}
			category := failure("", err).Category
			if errors.Is(err, runresult.ErrResetParse) {
				category = "parse"
			} else if category == "operation" {
				category = "transport"
			}
			row.Outcome = scen.ResetOutcomeError
			row.Actual = nil
			row.ErrorIDs = append(row.ErrorIDs, r.addError(check.ID, "", category, err))
			break
		}
		if err := r.schema.ValidateObservedResetValue(check, actual); err != nil {
			row.Outcome, row.Actual = scen.ResetOutcomeError, nil
			row.ErrorIDs = append(row.ErrorIDs, r.addError(check.ID, "", "parse", err))
			break
		}
		matched, err := r.schema.CompareResetValue(check, actual)
		if err != nil {
			row.Outcome, row.Actual = scen.ResetOutcomeError, nil
			row.ErrorIDs = append(row.ErrorIDs, r.addError(check.ID, "", "parse", err))
			break
		}
		row.Actual, row.Outcome = actual, scen.ResetMismatch
		if matched {
			row.Outcome = scen.ResetMatched
			break
		}
		timer := time.NewTimer(time.Duration(r.scope.Scenario().Timeouts.Poll))
		select {
		case <-ctx.Done():
			timer.Stop()
			r.addError("", "", failure("", ctx.Err()).Category, ctx.Err())
		case <-timer.C:
			continue
		}
		break
	}
	completed := time.Now().UTC()
	row.CompletedAt = &completed
	return row
}

// ReconcileReset authorizes reuse only after independently verified termination.
// It does not change the native execution report or contribute scenario specs.
func ReconcileReset(resolved ResolvedScenario, baseline scen.ResetBaseline, expected ResetContext,
	execution types.Report, receipt ResetReceipt, completed ResetCompletion,
) error {
	if err := PreflightReset(resolved, baseline, expected, execution); err != nil {
		return err
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode reset receipt: %w", err)
	}
	validated, err := ReadResetReceipt(bytes.NewReader(data), baseline, resolved.registry.Schema())
	if err != nil {
		return err
	}
	// Completion is independently joined by the owner. Host and runner wall
	// clocks are not comparable; identity, boundary and events bind the receipt.
	if completed.BoundaryConsumed || completed.ExitCode != 0 || completed.FinishedAt.IsZero() ||
		validated.Identity != expected.Identity || validated.Generation != expected.Generation ||
		validated.Baseline != expected.Baseline || validated.ResetID != expected.ResetID ||
		validated.Boundary != expected.Boundary || !validated.CaseFinishedAt.Equal(expected.CaseFinishedAt) ||
		len(validated.Errors) != 0 {
		return inputError("reset", "stale, consumed, incomplete or failed reset boundary")
	}
	for _, row := range validated.Observations {
		if row.Outcome != scen.ResetMatched || !slices.Contains(expected.BoundTargets, row.Target) {
			return inputError("reset", "unmatched observation or unbound container")
		}
	}
	return reconcileResetResources(resolved, validated, completed.Events)
}

func reconcileResetResources(resolved ResolvedScenario, receipt ResetReceipt, events []ResetEvent) error {
	acquired := []ResetEvent{}
	released := make(map[string]ResetEvent)
	for _, event := range events {
		if event.ResetID != receipt.ResetID || event.Boundary != receipt.Boundary {
			return inputError("events", "stale reset acquisition event")
		}
		switch event.Type {
		case "acquired":
			if slices.ContainsFunc(acquired, func(row ResetEvent) bool { return row.ResourceID == event.ResourceID }) {
				return inputError("events", "duplicate acquisition")
			}
			acquired = append(acquired, event)
		case ResourceReleased:
			if _, duplicate := released[event.ResourceID]; duplicate ||
				!slices.ContainsFunc(acquired, func(row ResetEvent) bool { return row.ResourceID == event.ResourceID }) {
				return inputError("events", "duplicate release or release before acquisition")
			}
			released[event.ResourceID] = event
		default:
			return inputError("events", "failed or unknown resource event")
		}
	}
	if len(acquired) != len(receipt.Resources) || len(released) != len(acquired) {
		return inputError("resources", "receipt does not reconcile with every acquisition and release")
	}
	for i, row := range receipt.Resources {
		original := acquired[i]
		last := released[row.ID]
		if row.State != ResourceReleased || !row.Joined || row.ID != original.ResourceID ||
			row.OwnerResetID != original.ResetID || row.Kind != original.Kind ||
			row.Target != original.Target || !reflect.DeepEqual(row.Locator, original.Locator) ||
			!row.AcquiredAt.Equal(original.At) || last.Target != row.Target || last.Kind != row.Kind ||
			!reflect.DeepEqual(last.Locator, row.Locator) || row.ReleasedAt == nil || !row.ReleasedAt.Equal(last.At) {
			return inputError("resources", "resource identity changed, work unjoined or release missing")
		}
		if err := resetResourceBinding(resolved, row); err != nil {
			return err
		}
	}
	return nil
}

func (r *resetExecution) restore(ctx context.Context, registry Bindings, baseline scen.ResetBaseline) error {
	actions := []Action{}
	for _, domain := range baseline.Domains {
		prepared, err := registry.resetHandlers[domain.Name].PrepareRestore(ctx, r.scope, baseline)
		if err != nil {
			return err
		}
		actions = append(actions, prepared...)
	}
	if r.err != nil {
		return r.err
	}
	var failures []error
	for _, action := range actions {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		call, cancel := context.WithTimeout(ctx, time.Duration(r.scope.Scenario().Timeouts.Action))
		_, err := action(call)
		err = errors.Join(err, call.Err())
		cancel()
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

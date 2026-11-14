package boro

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"

	runresult "boro/result"

	scen "boro/scenario"
)

// Bindings is the immutable typed execution table supplied to Capability.Register.
// Its zero value cannot install handlers. Only Compose creates initialized bindings.
type Bindings struct {
	operations    map[string]registration
	schema        scen.Schema
	resetHandlers map[string]ResetHandler
	initialized   bool
}

func newBindings(schema scen.Schema) Bindings {
	return Bindings{schema: schema, initialized: true}
}

func (r Bindings) Schema() scen.Schema { return r.schema }

// Action performs one mutation without retries and honors its context.
type Action func(context.Context) (runresult.Action, error)

// Observer performs one bounded sample. Only a nil error permits a comparison.
type Observer func(context.Context) (runresult.Observation, error)

// ActionPreparer binds one step to invocation-local state and clients without
// mutating the SUT or starting traffic.
type ActionPreparer[I any] func(context.Context, *RunScope, I) (Action, error)

// ObservationFunc samples and compares one typed expectation.
type ObservationFunc[W any] func(context.Context, W) (runresult.Observation, error)

// ObserverPreparer binds one observation step to invocation-local state and clients.
type ObserverPreparer[I, W any] func(context.Context, *RunScope, I) (ObservationFunc[W], error)

// ActionBinding creates one preparer per invocation, shared by its steps.
// NewPrepare only creates state. Client acquisition belongs in the preparer.
type ActionBinding[I any] struct {
	Spec       scen.OperationSpec
	NewPrepare func() ActionPreparer[I]
}

// ObserverBinding owns comparison semantics outside the scheduler.
// NewPrepare only creates invocation-local state, shared by its observation steps.
type ObserverBinding[I, W any] struct {
	Spec       scen.OperationSpec
	NewPrepare func() ObserverPreparer[I, W]
}

type PreparedOperation struct {
	Action  Action
	Observe Observer
}

// BindAction returns a new registry, leaving the original usable by other runs.
func BindAction[I any](registry Bindings, entry ActionBinding[I]) (Bindings, error) {
	declared := registry.descriptor(entry.Spec.Name)
	reg := registration{Descriptor: declared}
	reg.Input, reg.Want = reflect.TypeFor[I](), nil
	if entry.NewPrepare == nil {
		return Bindings{}, inputError("registry", "action requires an invocation factory")
	}
	reg.invocation = func() prepareFactory {
		prepare := entry.NewPrepare()
		return func(ctx context.Context, scope *RunScope, step scen.Step) (PreparedOperation, error) {
			input, ok := step.Input.(I)
			if !ok {
				return PreparedOperation{}, inputError("with", "registered action payload type mismatch")
			}
			if prepare == nil {
				return PreparedOperation{}, inputError("registry", "invocation factory returned no preparation")
			}
			action, err := prepare(ctx, scope, scen.Clone(input))
			if err != nil {
				return PreparedOperation{}, err
			}
			if action == nil {
				return PreparedOperation{}, inputError("registry", "action factory returned no handler")
			}
			return PreparedOperation{Action: action}, nil
		}
	}
	return registry.with(entry.Spec, reg)
}

// BindObserver installs a typed observation factory without scheduler dispatch changes.
func BindObserver[I, W any](registry Bindings, entry ObserverBinding[I, W]) (Bindings, error) {
	if entry.Spec.ResetDomain != "" {
		return Bindings{}, inputError("registry", "observer cannot declare an action reset domain")
	}
	declared := registry.descriptor(entry.Spec.Name)
	reg := registration{Descriptor: declared}
	reg.Input, reg.Want = reflect.TypeFor[I](), reflect.TypeFor[W]()
	if entry.NewPrepare == nil {
		return Bindings{}, inputError("registry", "observer requires an invocation factory")
	}
	reg.invocation = func() prepareFactory {
		prepare := entry.NewPrepare()
		return func(ctx context.Context, scope *RunScope, step scen.Step) (PreparedOperation, error) {
			input, inputOK := step.Input.(I)
			want, wantOK := step.Want.(W)
			if !inputOK || !wantOK {
				return PreparedOperation{}, inputError("with", "registered observer payload type mismatch")
			}
			if prepare == nil {
				return PreparedOperation{}, inputError("registry", "invocation factory returned no preparation")
			}
			observe, err := prepare(ctx, scope, scen.Clone(input))
			if err != nil {
				return PreparedOperation{}, err
			}
			if observe == nil {
				return PreparedOperation{}, inputError("registry", "observer factory returned no handler")
			}
			want = scen.Clone(want)
			return PreparedOperation{Observe: func(ctx context.Context) (runresult.Observation, error) {
				return observe(ctx, scen.Clone(want))
			}}, nil
		}
	}
	return registry.with(entry.Spec, reg)
}

func (r Bindings) with(spec scen.OperationSpec, reg registration) (Bindings, error) {
	if !r.initialized {
		return Bindings{}, inputError("bindings", "bindings must be supplied by Compose")
	}
	if r.HasExecution(spec.Name) {
		return Bindings{}, inputError("registry", "execution handler already installed: "+spec.Name)
	}
	declared, ok := r.schema.Lookup(spec.Name)
	if !ok {
		return Bindings{}, bindingError("operation must be declared before binding: " + spec.Name)
	}
	if declared.Spec() != spec || declared.Input != reg.Input || declared.Want != reg.Want {
		return Bindings{}, bindingError("binding changes an existing operation schema: " + spec.Name)
	}
	entries := maps.Clone(r.operations)
	if entries == nil {
		entries = make(map[string]registration)
	}
	entries[spec.Name] = reg
	r.operations = entries
	return r, nil
}

func (r Bindings) require(steps []scen.Step) error {
	for _, step := range steps {
		if step.Operation != "" && !r.HasExecution(step.Operation) {
			return inputError("operations", fmt.Sprintf("execution handlers unavailable: %s (step %s)", step.Operation, step.ID))
		}
		for _, branch := range step.Branches {
			if err := r.require(branch.Steps); err != nil {
				return err
			}
		}
	}
	return nil
}

type (
	prepareFactory func(context.Context, *RunScope, scen.Step) (PreparedOperation, error)
	registration   struct {
		scen.Descriptor

		prepare    prepareFactory
		invocation func() prepareFactory
	}
)

func (r Bindings) descriptor(name string) scen.Descriptor { d, _ := r.schema.Lookup(name); return d }

func (r Bindings) HasExecution(name string) bool {
	e := r.operations[name]
	return e.prepare != nil || e.invocation != nil
}

// Prepare prepares a single operation for a caller-owned scope. Run materializes
// invocation factories once for its full ordered/parallel step tree.
func (r Bindings) Prepare(ctx context.Context, scope *RunScope, step scen.Step) (PreparedOperation, error) {
	if err := ctx.Err(); err != nil {
		return PreparedOperation{}, fmt.Errorf("preparation canceled: %w", err)
	}
	e := r.operations[step.Operation]
	if e.invocation != nil {
		e.prepare = e.invocation()
	}
	if e.prepare == nil {
		return PreparedOperation{}, inputError("operations", "execution handlers unavailable: "+step.Operation)
	}
	return e.prepare(ctx, scope, step)
}

func (r Bindings) forInvocation() Bindings {
	entries := maps.Clone(r.operations)
	for name, entry := range entries {
		if entry.invocation != nil {
			entry.prepare = entry.invocation()
			entry.invocation = nil
			entries[name] = entry
		}
	}
	r.operations = entries
	return r
}

func (r Bindings) RequireExecution(s scen.Scenario) error {
	d := s.Snapshot()
	for _, steps := range [][]scen.Step{d.Setup, d.Steps, d.Teardown} {
		if err := r.require(steps); err != nil {
			return err
		}
	}
	return nil
}
func (r Bindings) ResetSupported(domain string) bool { _, ok := r.resetHandlers[domain]; return ok }
func (r Bindings) ResetDomains(s scen.Scenario) ([]string, error) {
	if err := r.RequireExecution(s); err != nil {
		return nil, err
	}
	names, _ := scen.Operations(s.Snapshot())
	domains := []string{}
	for _, name := range names {
		d := r.descriptor(name)
		if d.Want != nil || d.ResetDomain == scen.ResetReadOnly {
			continue
		}
		if d.ResetDomain == "" || !r.ResetSupported(d.ResetDomain) {
			return nil, inputError("reset_domain", "unavailable action reset domain: "+name)
		}
		if !slices.Contains(domains, d.ResetDomain) {
			domains = append(domains, d.ResetDomain)
		}
	}
	slices.Sort(domains)
	return domains, nil
}

// ResetHandler supplies domain preflight, typed observation and restoration preparation.
// The engine owns timeouts, observation comparison, receipts and resource cleanup.
type ResetHandler struct {
	Validate       func(ResolvedScenario, scen.ResetBaseline) error
	Prepare        func(context.Context, *RunScope, scen.ResetBaseline, scen.ExpectedCheck) (func(context.Context) (scen.ResetValue, error), error)
	PrepareRestore func(context.Context, *RunScope, scen.ResetBaseline) ([]Action, error)
}

func BindReset(r Bindings, domain string, handler ResetHandler) (Bindings, error) {
	if !r.initialized {
		return Bindings{}, inputError("bindings", "bindings must be supplied by Compose")
	}
	if domain == "" || handler.Validate == nil || handler.Prepare == nil || handler.PrepareRestore == nil || r.ResetSupported(domain) {
		return Bindings{}, inputError("reset_domain", "a unique domain and complete reset handler are required")
	}
	handlers := maps.Clone(r.resetHandlers)
	if handlers == nil {
		handlers = make(map[string]ResetHandler)
	}
	handlers[domain] = handler
	r.resetHandlers = handlers
	return r, nil
}

func inputError(field, message string) *scen.ValidationError {
	return &scen.ValidationError{Field: field, Message: message}
}

func bindingError(message string) error {
	return fmt.Errorf("%w: %w", inputError("bindings", message), scen.ErrInput)
}

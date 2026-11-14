// Package counter is a copyable capability with typed data, injected backends and
// no scenario scheduling, polling, or reporting.
package counter

import (
	"context"
	"errors"
	"fmt"
	"time"

	runresult "boro/result"

	"boro"
	"boro/examples/counter/schema"
	"boro/scenario"
)

// BackendFactory acquires a backend after concrete environment bindings are known.
// The managed scope owns each Counter opened from it, never the backend itself.
type BackendFactory func(context.Context, boro.EnvironmentManifest) (Backend, error)

// Capability installs counter declarations and behavior without acquiring backends.
type Capability struct {
	factory BackendFactory
}

func NewCapability(factory BackendFactory) (*Capability, error) {
	if factory == nil {
		return nil, fmt.Errorf("counter backend factory is required: %w", scenario.ErrInput)
	}
	return &Capability{factory: factory}, nil
}

func (*Capability) Declare(base scenario.Schema) (scenario.Schema, error) {
	declared, err := schema.Register(base)
	if err != nil {
		return scenario.Schema{}, fmt.Errorf("declare counter: %w", err)
	}
	return declared, nil
}

// Register binds previously declared operations. Increment has no reset handler:
// a provider must recycle its environment instead of claiming unsupported reuse.
func (c *Capability) Register(bindings boro.Bindings) (boro.Bindings, error) {
	bindings, err := boro.BindAction(bindings, boro.ActionBinding[schema.IncrementInput]{
		Spec: schema.Increment().Spec(), NewPrepare: c.incrementPreparer,
	})
	if err != nil {
		return boro.Bindings{}, fmt.Errorf("register counter increment: %w", err)
	}
	bindings, err = boro.BindObserver(bindings, boro.ObserverBinding[schema.ValueInput, schema.ValueWant]{
		Spec: schema.Value().Spec(), NewPrepare: c.valuePreparer,
	})
	if err != nil {
		return boro.Bindings{}, fmt.Errorf("register counter observation: %w", err)
	}
	return bindings, nil
}

type runtime struct {
	factory   BackendFactory
	operation string
	backend   Backend
	counters  map[scenario.Ref[Spec]]*Counter
}

func (c *Capability) incrementPreparer() boro.ActionPreparer[schema.IncrementInput] {
	r := &runtime{factory: c.factory, operation: "increment", counters: make(map[scenario.Ref[Spec]]*Counter)}
	return r.prepareIncrement
}

func (c *Capability) valuePreparer() boro.ObserverPreparer[schema.ValueInput, schema.ValueWant] {
	r := &runtime{factory: c.factory, operation: "value", counters: make(map[scenario.Ref[Spec]]*Counter)}
	return r.prepareValue
}

func (r *runtime) prepareIncrement(ctx context.Context, scope *boro.RunScope, input schema.IncrementInput) (boro.Action, error) {
	counter, err := r.counterFor(ctx, scope, input.Counter)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (runresult.Action, error) {
		if err := counter.Add(ctx, input.By); err != nil {
			return runresult.Action{}, fmt.Errorf("increment counter: %w", err)
		}
		return runresult.Action{Detail: fmt.Sprintf("incremented by %d", input.By)}, nil
	}, nil
}

func (r *runtime) prepareValue(ctx context.Context, scope *boro.RunScope, input schema.ValueInput) (boro.ObservationFunc[schema.ValueWant], error) {
	counter, err := r.counterFor(ctx, scope, input.Counter)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, want schema.ValueWant) (runresult.Observation, error) {
		value, err := counter.Value(ctx)
		if err != nil {
			return runresult.Observation{}, fmt.Errorf("read counter: %w", err)
		}
		return runresult.Observation{Matches: value == want.Value, Detail: fmt.Sprintf("value=%d, want=%d", value, want.Value)}, nil
	}, nil
}

func (r *runtime) counterFor(ctx context.Context, scope *boro.RunScope, ref scenario.Ref[Spec]) (*Counter, error) {
	// Preparation traverses the complete tree serially before mutations start.
	if counter, ok := r.counters[ref]; ok {
		return counter, nil
	}
	resource, err := scenario.ResolveResource(scope.Scenario(), ref)
	if err != nil {
		return nil, fmt.Errorf("resolve counter: %w", err)
	}
	if r.backend == nil {
		backend, err := r.factory(ctx, scope.Environment())
		if err != nil {
			return nil, fmt.Errorf("acquire counter backend: %w", err)
		}
		if backend == nil {
			return nil, fmt.Errorf("counter backend factory returned no backend: %w", scenario.ErrInput)
		}
		r.backend = backend
	}
	acquired, err := New(ctx, resource, r.backend)
	if err != nil {
		return nil, err
	}
	if err := scope.Own(boro.TemporaryResource{
		Identity: boro.ResourceIdentity{ID: "counter/" + r.operation + "/" + string(ref), Kind: boro.ResourceClient},
		Release:  acquired.Close,
	}); err != nil {
		return nil, fmt.Errorf("own counter: %w", errors.Join(err, closeUnowned(ctx, acquired)))
	}
	r.counters[ref] = acquired
	return acquired, nil
}

func closeUnowned(ctx context.Context, counter *Counter) error {
	// Failed ownership transfer must close even after caller cancellation.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := counter.Close(cleanup); err != nil {
		return fmt.Errorf("close unowned counter: %w", err)
	}
	return nil
}

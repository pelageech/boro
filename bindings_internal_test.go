package boro

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"

	runresult "github.com/pelageech/boro/result"

	"github.com/stretchr/testify/require"

	scen "github.com/pelageech/boro/scenario"
)

type registryInput struct {
	Values []int `yaml:"values"`
}

type registryWant struct {
	Values []int `yaml:"values"`
}

func registrySchema(t *testing.T) scen.Schema {
	t.Helper()
	action := scen.Action[registryInput](scen.OperationSpec{Capability: "vpp"}, func(scen.ValidationContext, registryInput) error { return nil })
	action.Name = "test.action"
	observer := scen.Observer[registryInput, registryWant](scen.OperationSpec{Capability: "vpp"}, func(scen.ValidationContext, registryInput, registryWant) error { return nil })
	observer.Name = "test.observer"
	schema, err := (scen.Schema{}).WithDescriptor(action)
	require.NoError(t, err)
	schema, err = schema.WithDescriptor(observer)
	require.NoError(t, err)
	return schema
}

func TestRegistryInvocationState(t *testing.T) {
	var actionFactories, observerFactories atomic.Int32
	registry, err := BindAction(newBindings(registrySchema(t)), ActionBinding[registryInput]{
		Spec: scen.OperationSpec{Name: "test.action", Capability: "vpp"},
		NewPrepare: func() ActionPreparer[registryInput] {
			actionFactories.Add(1)
			preparations, calls := 0, 0
			return func(context.Context, *RunScope, registryInput) (Action, error) {
				preparations++
				return func(context.Context) (runresult.Action, error) {
					calls++
					return runresult.Action{Detail: strconv.Itoa(preparations) + "/" + strconv.Itoa(calls)}, nil
				}, nil
			}
		},
	})
	require.NoError(t, err)
	registry, err = BindObserver(registry, ObserverBinding[registryInput, registryWant]{
		Spec: scen.OperationSpec{Name: "test.observer", Capability: "vpp"},
		NewPrepare: func() ObserverPreparer[registryInput, registryWant] {
			observerFactories.Add(1)
			preparations, calls := 0, 0
			return func(context.Context, *RunScope, registryInput) (ObservationFunc[registryWant], error) {
				preparations++
				return func(_ context.Context, want registryWant) (runresult.Observation, error) {
					calls++
					return runresult.Observation{
						Matches: preparations == 2 && calls == want.Values[0],
						Detail:  strconv.Itoa(calls),
					}, nil
				}, nil
			}
		},
	})
	require.NoError(t, err)
	require.Zero(t, actionFactories.Load(), "assembly must not materialize invocation state")
	require.Zero(t, observerFactories.Load())

	t.Run("concurrent invocations", func(t *testing.T) {
		for range 8 {
			t.Run("invocation", func(t *testing.T) {
				t.Parallel()
				for range 2 {
					invocation := registry.forInvocation()
					actions := make([]Action, 3)
					for i := range actions {
						prepared, err := invocation.Prepare(t.Context(), nil, scen.Step{
							Operation: "test.action", Input: registryInput{},
						})
						require.NoError(t, err)
						require.Nil(t, prepared.Observe)
						actions[i] = prepared.Action
					}
					observers := make([]Observer, 2)
					for i := range observers {
						prepared, err := invocation.Prepare(t.Context(), nil, scen.Step{
							Operation: "test.observer", Input: registryInput{}, Want: registryWant{Values: []int{i + 1}},
						})
						require.NoError(t, err)
						require.Nil(t, prepared.Action)
						observers[i] = prepared.Observe
					}
					for i, action := range actions {
						result, err := action(t.Context())
						require.NoError(t, err)
						require.Equal(t, "3/"+strconv.Itoa(i+1), result.Detail)
					}
					for i, observe := range observers {
						result, err := observe(t.Context())
						require.NoError(t, err)
						require.True(t, result.Matches)
						require.Equal(t, strconv.Itoa(i+1), result.Detail)
					}
				}
			})
		}
	})
	require.EqualValues(t, 16, actionFactories.Load())
	require.EqualValues(t, 16, observerFactories.Load())
}

func TestRegistryAssemblyValidation(t *testing.T) {
	action := ActionBinding[registryInput]{
		Spec: scen.OperationSpec{Name: "test.action", Capability: "vpp"},
		NewPrepare: func() ActionPreparer[registryInput] {
			t.Fatal("assembly must not invoke a factory")
			return nil
		},
	}
	observer := ObserverBinding[registryInput, registryWant]{
		Spec: scen.OperationSpec{Name: "test.observer", Capability: "vpp"},
		NewPrepare: func() ObserverPreparer[registryInput, registryWant] {
			t.Fatal("assembly must not invoke a factory")
			return nil
		},
	}
	t.Run("zero bindings", func(t *testing.T) {
		_, err := BindAction(Bindings{}, action)
		require.ErrorContains(t, err, "bindings must be supplied by Compose")
		_, err = BindObserver(Bindings{}, observer)
		require.ErrorContains(t, err, "bindings must be supplied by Compose")
	})
	t.Run("undeclared operations", func(t *testing.T) {
		a, o := action, observer
		a.Spec.Name, o.Spec.Name = "test.undeclared_action", "test.undeclared_observer"
		_, err := BindAction(newBindings(registrySchema(t)), a)
		require.ErrorContains(t, err, "operation must be declared before binding")
		_, err = BindObserver(newBindings(registrySchema(t)), o)
		require.ErrorContains(t, err, "operation must be declared before binding")
	})
	t.Run("descriptor metadata", func(t *testing.T) {
		for _, mutate := range []func(*scen.OperationSpec){
			func(spec *scen.OperationSpec) { spec.Capability = "other" },
			func(spec *scen.OperationSpec) { spec.Role = "other" },
		} {
			a, o := action, observer
			mutate(&a.Spec)
			mutate(&o.Spec)
			_, err := BindAction(newBindings(registrySchema(t)), a)
			require.ErrorContains(t, err, "binding changes an existing operation schema")
			_, err = BindObserver(newBindings(registrySchema(t)), o)
			require.ErrorContains(t, err, "binding changes an existing operation schema")
		}
		a := action
		a.Spec.ResetDomain = scen.ResetReadOnly
		_, err := BindAction(newBindings(registrySchema(t)), a)
		require.ErrorContains(t, err, "binding changes an existing operation schema")
	})
	t.Run("declared selector and validation metadata survive binding", func(t *testing.T) {
		rejected := errors.New("declared contextual validator")
		descriptor := scen.Action[registryInput](scen.OperationSpec{Name: "test.custom", Capability: "vpp"},
			func(ctx scen.ValidationContext, _ registryInput) error {
				if ctx.Profile.Name != "custom" {
					t.Fatal("binding lost validation context")
				}
				return rejected
			})
		descriptor.ValidateSelectors, descriptor.SelectorCapability = false, "custom"
		schema, err := (scen.Schema{}).WithDescriptor(descriptor)
		require.NoError(t, err)
		bound, err := BindAction(newBindings(schema), ActionBinding[registryInput]{
			Spec: descriptor.Spec(), NewPrepare: action.NewPrepare,
		})
		require.NoError(t, err)
		actual := bound.operations[descriptor.Name]
		require.False(t, actual.ValidateSelectors)
		require.Equal(t, "custom", actual.SelectorCapability)
		require.ErrorIs(t, actual.Validate(scen.ValidationContext{Profile: scen.ProfileDeclaration{Name: "custom"}},
			scen.Step{Input: registryInput{}}), rejected)
	})
	for _, tc := range []struct {
		name string
		spec scen.OperationSpec
	}{
		{"empty name", scen.OperationSpec{Capability: "vpp"}},
		{"invalid name", scen.OperationSpec{Name: "invalid name", Capability: "vpp"}},
		{"empty capability", scen.OperationSpec{Name: "test.operation"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, o := action, observer
			a.Spec, o.Spec = tc.spec, tc.spec
			_, err := BindAction(newBindings(registrySchema(t)), a)
			require.ErrorAs(t, err, new(*scen.ValidationError))
			require.ErrorIs(t, err, scen.ErrInput)
			_, err = BindObserver(newBindings(registrySchema(t)), o)
			require.ErrorAs(t, err, new(*scen.ValidationError))
			require.ErrorIs(t, err, scen.ErrInput)
		})
	}
	t.Run("nil invocation factories", func(t *testing.T) {
		a, o := action, observer
		a.NewPrepare, o.NewPrepare = nil, nil
		_, err := BindAction(newBindings(registrySchema(t)), a)
		require.ErrorContains(t, err, "requires an invocation factory")
		_, err = BindObserver(newBindings(registrySchema(t)), o)
		require.ErrorContains(t, err, "requires an invocation factory")
	})
	t.Run("observer reset domain", func(t *testing.T) {
		o := observer
		o.Spec.ResetDomain = scen.ResetReadOnly
		_, err := BindObserver(newBindings(registrySchema(t)), o)
		require.ErrorContains(t, err, "observer cannot declare an action reset domain")
	})
	t.Run("incomplete payload shapes", func(t *testing.T) {
		_, err := BindAction(newBindings(registrySchema(t)), ActionBinding[any]{
			Spec: action.Spec, NewPrepare: func() ActionPreparer[any] { return nil },
		})
		require.ErrorAs(t, err, new(*scen.ValidationError))
		require.ErrorIs(t, err, scen.ErrInput)
		_, err = BindAction(newBindings(registrySchema(t)), ActionBinding[*registryInput]{
			Spec: action.Spec, NewPrepare: func() ActionPreparer[*registryInput] { return nil },
		})
		require.ErrorAs(t, err, new(*scen.ValidationError))
		require.ErrorIs(t, err, scen.ErrInput)
		_, err = BindObserver(newBindings(registrySchema(t)), ObserverBinding[registryInput, any]{
			Spec: observer.Spec, NewPrepare: func() ObserverPreparer[registryInput, any] { return nil },
		})
		require.ErrorAs(t, err, new(*scen.ValidationError))
		require.ErrorIs(t, err, scen.ErrInput)
		_, err = BindObserver(newBindings(registrySchema(t)), ObserverBinding[registryInput, *registryWant]{
			Spec: observer.Spec, NewPrepare: func() ObserverPreparer[registryInput, *registryWant] { return nil },
		})
		require.ErrorAs(t, err, new(*scen.ValidationError))
		require.ErrorIs(t, err, scen.ErrInput)
	})
	t.Run("duplicate callbacks leave the source immutable", func(t *testing.T) {
		original := newBindings(registrySchema(t))
		registered, err := BindAction(original, action)
		require.NoError(t, err)
		require.False(t, original.HasExecution(action.Spec.Name))
		_, err = BindAction(registered, action)
		require.ErrorContains(t, err, "already installed")
		registered, err = BindObserver(registered, observer)
		require.NoError(t, err)
		_, err = BindObserver(registered, observer)
		require.ErrorContains(t, err, "already installed")
	})
}

func TestRegistryPreparationValidation(t *testing.T) {
	failed := errors.New("preparation failed")
	for _, tc := range []struct {
		name       string
		action     ActionPreparer[registryInput]
		observer   ObserverPreparer[registryInput, registryWant]
		diagnostic string
	}{
		{"nil preparer", nil, nil, "invocation factory returned no preparation"},
		{
			"nil handler",
			func(context.Context, *RunScope, registryInput) (Action, error) { return nil, nil },
			func(context.Context, *RunScope, registryInput) (ObservationFunc[registryWant], error) {
				return nil, nil
			},
			"factory returned no handler",
		},
		{
			"failed preparer",
			func(context.Context, *RunScope, registryInput) (Action, error) {
				return func(context.Context) (runresult.Action, error) { return runresult.Action{}, nil }, failed
			},
			func(context.Context, *RunScope, registryInput) (ObservationFunc[registryWant], error) {
				return func(context.Context, registryWant) (runresult.Observation, error) {
					return runresult.Observation{}, nil
				}, failed
			},
			failed.Error(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, err := BindAction(newBindings(registrySchema(t)), ActionBinding[registryInput]{
				Spec:       scen.OperationSpec{Name: "test.action", Capability: "vpp"},
				NewPrepare: func() ActionPreparer[registryInput] { return tc.action },
			})
			require.NoError(t, err)
			registry, err = BindObserver(registry, ObserverBinding[registryInput, registryWant]{
				Spec:       scen.OperationSpec{Name: "test.observer", Capability: "vpp"},
				NewPrepare: func() ObserverPreparer[registryInput, registryWant] { return tc.observer },
			})
			require.NoError(t, err)
			for _, step := range []scen.Step{
				{Operation: "test.action", Input: registryInput{}},
				{Operation: "test.observer", Input: registryInput{}, Want: registryWant{}},
			} {
				prepared, err := registry.forInvocation().Prepare(t.Context(), nil, step)
				require.ErrorContains(t, err, tc.diagnostic)
				require.Equal(t, PreparedOperation{}, prepared)
				if tc.name == "failed preparer" {
					require.ErrorIs(t, err, failed)
				}
				step.Input = registryWant{}
				_, err = registry.Prepare(t.Context(), nil, step)
				require.ErrorContains(t, err, "payload type mismatch")
				descriptor, ok := registry.Schema().Lookup(step.Operation)
				require.True(t, ok)
				require.ErrorContains(t, descriptor.Validate(scen.ValidationContext{}, step), "payload type mismatch")
			}
			_, err = registry.Prepare(t.Context(), nil, scen.Step{
				Operation: "test.observer", Input: registryInput{}, Want: registryInput{},
			})
			require.ErrorContains(t, err, "payload type mismatch")
		})
	}
}

func TestRegistryPreparedPayloadIsolation(t *testing.T) {
	registry, err := BindObserver(newBindings(registrySchema(t)), ObserverBinding[registryInput, registryWant]{
		Spec: scen.OperationSpec{Name: "test.observer", Capability: "vpp"},
		NewPrepare: func() ObserverPreparer[registryInput, registryWant] {
			return func(_ context.Context, _ *RunScope, input registryInput) (ObservationFunc[registryWant], error) {
				input.Values[0]++
				return func(_ context.Context, want registryWant) (runresult.Observation, error) {
					matches := input.Values[0] == 2 && want.Values[0] == 1
					want.Values[0]++
					return runresult.Observation{Matches: matches}, nil
				}, nil
			}
		},
	})
	require.NoError(t, err)
	input, want := registryInput{Values: []int{1}}, registryWant{Values: []int{1}}
	prepared, err := registry.Prepare(t.Context(), nil, scen.Step{
		Operation: "test.observer", Input: input, Want: want,
	})
	require.NoError(t, err)
	require.Equal(t, 1, input.Values[0])
	input.Values[0], want.Values[0] = 9, 9
	for range 2 {
		observation, err := prepared.Observe(t.Context())
		require.NoError(t, err)
		require.True(t, observation.Matches)
	}
}

func TestRegistryValidatorIsolation(t *testing.T) {
	rejected := errors.New("typed validation rejected input")
	input, want := registryInput{Values: []int{1}}, registryWant{Values: []int{1}}
	action := scen.Action[registryInput](scen.OperationSpec{Capability: "vpp"}, func(_ scen.ValidationContext, value registryInput) error {
		value.Values[0]++
		return rejected
	})
	action.Name = "test.action"
	observer := scen.Observer[registryInput, registryWant](scen.OperationSpec{Capability: "vpp"}, func(_ scen.ValidationContext, value registryInput, expected registryWant) error {
		value.Values[0]++
		expected.Values[0]++
		return rejected
	})
	observer.Name = "test.observer"
	schema, err := (scen.Schema{}).WithDescriptor(action)
	require.NoError(t, err)
	schema, err = schema.WithDescriptor(observer)
	require.NoError(t, err)
	registry, err := BindAction(newBindings(schema), ActionBinding[registryInput]{
		Spec:       action.Spec(),
		NewPrepare: func() ActionPreparer[registryInput] { return nil },
	})
	require.NoError(t, err)
	registry, err = BindObserver(registry, ObserverBinding[registryInput, registryWant]{
		Spec:       observer.Spec(),
		NewPrepare: func() ObserverPreparer[registryInput, registryWant] { return nil },
	})
	require.NoError(t, err)
	for _, name := range []string{"test.action", "test.observer"} {
		descriptor, ok := registry.Schema().Lookup(name)
		require.True(t, ok)
		for _, validate := range []func(scen.ValidationContext, scen.Step) error{descriptor.Validate, registry.operations[name].Validate} {
			require.ErrorIs(t, validate(scen.ValidationContext{}, scen.Step{Input: input, Want: want}), rejected)
			require.Equal(t, 1, input.Values[0])
			require.Equal(t, 1, want.Values[0])
			require.ErrorContains(t, validate(scen.ValidationContext{}, scen.Step{Input: registryWant{}, Want: want}), "payload type mismatch")
			if name == "test.observer" {
				require.ErrorContains(t, validate(scen.ValidationContext{}, scen.Step{Input: input, Want: registryInput{}}), "payload type mismatch")
			}
		}
	}
}

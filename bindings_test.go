package boro_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"

	runresult "boro/result"

	"github.com/stretchr/testify/require"

	"boro"
	scen "boro/scenario"
)

func TestInvocationFactoryIsIsolated(t *testing.T) {
	machine := newTestMachine()
	var factories, observerFactories atomic.Int32
	action := scen.Action[testMutation](scen.OperationSpec{Capability: "test"}, nil)
	action.Name = "test.local"
	observer := scen.Observer[testRead, testWant](scen.OperationSpec{Capability: "test"}, nil)
	observer.Name = "test.local_read"
	extra := &testCapability{
		descriptors: []scen.Descriptor{action, observer},
		register: func(registry boro.Bindings) (boro.Bindings, error) {
			registry, err := boro.BindAction(registry, boro.ActionBinding[testMutation]{
				Spec: scen.OperationSpec{Name: "test.local", Capability: "test"},
				NewPrepare: func() boro.ActionPreparer[testMutation] {
					factories.Add(1)
					calls := 0
					return func(context.Context, *boro.RunScope, testMutation) (boro.Action, error) {
						return func(context.Context) (runresult.Action, error) {
							calls++
							return runresult.Action{Detail: strconv.Itoa(calls)}, nil
						}, nil
					}
				},
			})
			if err != nil {
				return boro.Bindings{}, err
			}
			registry, err = boro.BindObserver(registry, boro.ObserverBinding[testRead, testWant]{
				Spec: scen.OperationSpec{Name: "test.local_read", Capability: "test"},
				NewPrepare: func() boro.ObserverPreparer[testRead, testWant] {
					observerFactories.Add(1)
					calls := 0
					return func(context.Context, *boro.RunScope, testRead) (boro.ObservationFunc[testWant], error) {
						return func(_ context.Context, want testWant) (runresult.Observation, error) {
							calls++
							return runresult.Observation{Matches: calls == want.Value, Detail: strconv.Itoa(calls)}, nil
						}, nil
					}
				},
			})
			return registry, err
		},
	}
	registry := machine.composition(t, extra)
	resolved := resolveRun(t, runFixture{composition: registry, body: `setup:
  - {id: local-first, do: test.local, with: {key: state, value: 0}}
  - {id: local-second, do: test.local, with: {key: state, value: 0}}
steps:
  - {id: observe-first, expect: test.local_read, with: {key: state}, want: {value: 1}, wait: {mode: once}}
  - {id: observe-second, expect: test.local_read, with: {key: state}, want: {value: 2}, wait: {mode: once}}
`})
	require.Zero(t, factories.Load(), "loading and binding must not create invocation runtime")
	require.Zero(t, observerFactories.Load())
	for range 2 {
		t.Run("invocation", func(t *testing.T) {
			scope, prepared, err := resolved.PrepareInvocation(t.Context(), boro.ModeFull)
			require.NotNil(t, scope)
			defer func() {
				_, err := scope.Close(t.Context(), false)
				require.NoError(t, err)
			}()
			require.NoError(t, err)
			for i, id := range []string{"local-first", "local-second"} {
				result, err := prepared[id].Action(t.Context())
				require.NoError(t, err)
				require.Equal(t, strconv.Itoa(i+1), result.Detail)
			}
			for i, id := range []string{"observe-first", "observe-second"} {
				result, err := prepared[id].Observe(t.Context())
				require.NoError(t, err)
				require.True(t, result.Matches)
				require.Equal(t, strconv.Itoa(i+1), result.Detail)
			}
		})
	}
	require.EqualValues(t, 2, factories.Load(), "each invocation owns exactly one runtime shared by its steps")
	require.EqualValues(t, 2, observerFactories.Load())
}

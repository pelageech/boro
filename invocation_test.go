package boro_test

import (
	"context"
	"testing"
	"time"

	runresult "github.com/pelageech/boro/result"

	"github.com/stretchr/testify/require"

	"github.com/pelageech/boro"
	scen "github.com/pelageech/boro/scenario"
)

func TestPreparedRegistryExtensionsAreIndependent(t *testing.T) {
	machine := newTestMachine()
	original := machine.composition(t)
	entry := boro.ActionBinding[testMutation]{
		Spec: scen.OperationSpec{Name: "test.extra", Capability: "test"},
		NewPrepare: func() boro.ActionPreparer[testMutation] {
			return func(_ context.Context, _ *boro.RunScope, input testMutation) (boro.Action, error) {
				return func(context.Context) (runresult.Action, error) {
					machine.set(input.Key, input.Value)
					return runresult.Action{}, nil
				}, nil
			}
		},
	}
	descriptor := scen.Action[testMutation](entry.Spec, nil)
	descriptor.Name = entry.Spec.Name
	extra := &testCapability{
		descriptors: []scen.Descriptor{descriptor},
		register: func(bindings boro.Bindings) (boro.Bindings, error) {
			return boro.BindAction(bindings, entry)
		},
	}
	extended := machine.composition(t, extra)
	require.False(t, original.HasExecution("test.extra"))
	_, declared := original.Schema().Lookup("test.extra")
	require.False(t, declared)
	independent := machine.composition(t, extra)
	require.True(t, independent.HasExecution("test.extra"))
	resolved := resolveRun(t, runFixture{
		composition: extended,
		body:        "setup:\n  - {id: extra, do: test.extra, with: {key: state, value: 0}}\n" + singleRead,
	})
	scope, prepared, err := resolved.PrepareInvocation(t.Context(), boro.ModeFull)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
		defer cancel()
		_, err := scope.Close(ctx, false)
		require.NoError(t, err)
	}()
	_, err = prepared["extra"].Action(t.Context())
	require.NoError(t, err)
	observation, err := prepared["check"].Observe(t.Context())
	require.NoError(t, err)
	require.True(t, observation.Matches)
}

func TestPreparedOwnershipIsIsolated(t *testing.T) {
	machine := newTestMachine()
	releases := 0
	machine.prepare = func(_ context.Context, scope *boro.RunScope) error {
		return scope.Own(boro.TemporaryResource{
			Identity: boro.ResourceIdentity{ID: "same-local-client", Kind: boro.ResourceClient},
			Release:  func(context.Context) error { releases++; return nil },
		})
	}
	resolved := resolveRun(t, runFixture{
		composition: machine.composition(t),
		body:        "setup:\n  - {id: setup, do: test.set, with: {key: state, value: 0}}\n" + singleRead,
	})
	for range 2 {
		scope, _, err := resolved.PrepareInvocation(t.Context(), boro.ModeFull)
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
		resources, err := scope.Close(ctx, false)
		cancel()
		require.NoError(t, err)
		require.Len(t, resources, 1)
		require.Equal(t, boro.ResourceReleased, resources[0].State)
	}
	require.Equal(t, 2, releases)
}

func TestPrepareRejectsMissingHandlerBeforeClients(t *testing.T) {
	machine := newTestMachine()
	resolved := resolveRun(t, runFixture{composition: machine.composition(t), body: `setup:
  - {id: setup, do: test.set, with: {key: state, value: 1}}
steps:
  - {id: unsupported, expect: test.status, with: {on: {role: test-status, match: all}}, want: {accept: [healthy]}, wait: {mode: once}}
`})
	scope, prepared, err := resolved.PrepareInvocation(t.Context(), boro.ModeFull)
	require.ErrorContains(t, err, "test.status")
	require.Nil(t, scope)
	require.Nil(t, prepared)
	require.Zero(t, machine.preparations)
	require.Zero(t, machine.value("state"))
}

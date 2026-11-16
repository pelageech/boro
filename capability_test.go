package boro

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pelageech/boro/scenario"
)

type testCapability struct {
	declare  func(scenario.Schema) (scenario.Schema, error)
	register func(Bindings) (Bindings, error)
}

func (c *testCapability) Declare(s scenario.Schema) (scenario.Schema, error) {
	if c.declare == nil {
		return s, nil
	}
	return c.declare(s)
}

func (c *testCapability) Register(r Bindings) (Bindings, error) {
	if c.register == nil {
		return r, nil
	}
	return c.register(r)
}

func TestComposeDeclaresBeforeBinding(t *testing.T) {
	descriptor := scenario.Action[contractInput](scenario.OperationSpec{Capability: "test", ResetDomain: scenario.ResetReadOnly}, nil)
	descriptor.Name = "test.composed"
	runtime := &testCapability{register: func(r Bindings) (Bindings, error) {
		if _, ok := r.Schema().Lookup(descriptor.Name); !ok {
			return Bindings{}, errors.New("declarations unavailable during registration")
		}
		return BindAction(r, ActionBinding[contractInput]{
			Spec: descriptor.Spec(),
			NewPrepare: func() ActionPreparer[contractInput] {
				t.Fatal("composition materialized an execution factory")
				return nil
			},
		})
	}}
	declarations := &testCapability{declare: func(s scenario.Schema) (scenario.Schema, error) {
		descriptor.Validate = func(scenario.ValidationContext, scenario.Step) error {
			t.Fatal("composition called a validator")
			return nil
		}
		return s.WithDescriptor(descriptor)
	}}
	composition, err := Compose("test.composed.v1", runtime, declarations)
	require.NoError(t, err)
	require.True(t, composition.HasExecution(descriptor.Name))
	_, declared := composition.Schema().Lookup(descriptor.Name)
	require.True(t, declared)
	require.Equal(t, "test.composed.v1", composition.Contract().Revision)
	expected, err := newContract(composition.Schema(), composition.bindings, "test.composed.v1")
	require.NoError(t, err)
	require.NoError(t, expected.RequireCompatible(composition.Contract()))
}

func TestComposeRejectsInvalidContributions(t *testing.T) {
	descriptor := scenario.Action[contractInput](scenario.OperationSpec{Capability: "test", ResetDomain: scenario.ResetReadOnly}, nil)
	descriptor.Name = "test.undeclared"
	failure := errors.New("capability failed")
	var typedNil *testCapability
	for _, test := range []struct {
		name         string
		capabilities []Capability
		revision     string
		want         error
	}{
		{"missing revision", nil, "", ErrContract},
		{"nil capability", []Capability{nil}, "test.v1", scenario.ErrInput},
		{"typed nil capability", []Capability{typedNil}, "test.v1", scenario.ErrInput},
		{"declaration failure", []Capability{&testCapability{
			declare: func(scenario.Schema) (scenario.Schema, error) { return scenario.Schema{}, failure },
		}}, "test.v1", failure},
		{"registration failure", []Capability{&testCapability{
			register: func(Bindings) (Bindings, error) { return Bindings{}, failure },
		}}, "test.v1", failure},
		{"undeclared runtime schema", []Capability{&testCapability{
			register: func(r Bindings) (Bindings, error) {
				return BindAction(r, ActionBinding[contractInput]{
					Spec: descriptor.Spec(),
					NewPrepare: func() ActionPreparer[contractInput] {
						return func(context.Context, *RunScope, contractInput) (Action, error) { return nil, nil }
					},
				})
			},
		}}, "test.v1", scenario.ErrInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compose(test.revision, test.capabilities...)
			require.ErrorIs(t, err, test.want)
		})
	}
}

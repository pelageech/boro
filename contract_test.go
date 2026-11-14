package boro

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	scen "boro/scenario"
)

type contractInput struct {
	Value int `yaml:"value" json:"value"`
}

func contractRegistry(t *testing.T) (scen.Schema, Bindings) {
	t.Helper()
	descriptor := scen.Action[contractInput](scen.OperationSpec{Capability: "test", ResetDomain: scen.ResetReadOnly}, func(scen.ValidationContext, contractInput) error {
		t.Fatal("contract construction called a validator")
		return nil
	})
	descriptor.Name = "test.contract"
	schema, err := scen.WithResource(scen.Schema{}, scen.ResourceRegistration[contractInput]{Spec: scen.ResourceSpec{Name: "test.resource", Capability: "test"}})
	require.NoError(t, err)
	schema, err = schema.WithDescriptor(descriptor)
	require.NoError(t, err)
	registry, err := BindAction(newBindings(schema), ActionBinding[contractInput]{
		Spec: descriptor.Spec(),
		NewPrepare: func() ActionPreparer[contractInput] {
			t.Fatal("contract construction called a factory")
			return func(context.Context, *RunScope, contractInput) (Action, error) { return nil, nil }
		},
	})
	require.NoError(t, err)
	return schema, registry
}

func TestCompositionContract(t *testing.T) {
	schema, registry := contractRegistry(t)
	owner, err := newContract(schema, registry, "test.v1")
	require.NoError(t, err)
	same, err := newContract(schema, registry, "test.v1")
	require.NoError(t, err)
	require.NoError(t, owner.RequireCompatible(same))
	require.Equal(t, owner, same)

	t.Run("explicit semantics", func(t *testing.T) {
		_, err := newContract(schema, registry, "")
		require.ErrorIs(t, err, ErrContract)
		revised, err := newContract(schema, registry, "test.v2")
		require.NoError(t, err)
		require.Equal(t, owner.SchemaSHA256, revised.SchemaSHA256)
		require.Equal(t, owner.RuntimeSHA256, revised.RuntimeSHA256)
		require.ErrorContains(t, owner.RequireCompatible(revised), "revision")
	})
	t.Run("pure runtime mismatch", func(t *testing.T) {
		_, err := newContract(scen.Schema{}, registry, "test.v1")
		require.ErrorContains(t, err, "pure and runtime schema")
	})
	t.Run("missing execution", func(t *testing.T) {
		missing, err := newContract(schema, newBindings(schema), "test.v1")
		require.NoError(t, err)
		require.ErrorContains(t, owner.RequireCompatible(missing), "execution/reset")
	})
	t.Run("reset coverage", func(t *testing.T) {
		other := registry
		other.resetHandlers = map[string]ResetHandler{"test.reset": {}}
		changed, err := newContract(schema, other, "test.v1")
		require.NoError(t, err)
		require.ErrorContains(t, owner.RequireCompatible(changed), "execution/reset")
	})
	t.Run("incomplete identity", func(t *testing.T) {
		require.ErrorIs(t, owner.RequireCompatible(Contract{}), ErrContract)
		invalid := owner
		invalid.SchemaSHA256 = "invalid"
		require.ErrorIs(t, owner.RequireCompatible(invalid), ErrContract)
	})
}

func TestCompositionContractShape(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field reflect.StructField
	}{
		{"field type", reflect.StructField{Name: "Value", Type: reflect.TypeFor[string](), Tag: `yaml:"value"`}},
		{"YAML tag", reflect.StructField{Name: "Value", Type: reflect.TypeFor[int](), Tag: `yaml:"other"`}},
		{"validation tag", reflect.StructField{Name: "Value", Type: reflect.TypeFor[int](), Tag: `yaml:"value" min:"2"`}},
		{"JSON tag", reflect.StructField{Name: "Value", Type: reflect.TypeFor[int](), Tag: `yaml:"value" json:"other"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: reflect.TypeFor[int](), Tag: `yaml:"value"`}})
			changed := reflect.StructOf([]reflect.StructField{tc.field})
			for _, resource := range []bool{false, true} {
				contracts := make([]Contract, 0, 2)
				for _, shape := range []reflect.Type{original, changed} {
					schema := scen.Schema{}
					var err error
					if resource {
						schema, err = schema.WithResourceDescriptor(scen.ResourceDescriptor{
							ResourceSpec: scen.ResourceSpec{Name: "test.resource", Capability: "test"}, Type: shape,
						})
					} else {
						schema, err = schema.WithDescriptor(scen.Descriptor{Name: "test.shape", Capability: "test", Input: shape})
					}
					require.NoError(t, err)
					contract, err := newContract(schema, newBindings(schema), "test.v1")
					require.NoError(t, err)
					contracts = append(contracts, contract)
				}
				require.ErrorContains(t, contracts[0].RequireCompatible(contracts[1]), "schema shapes")
			}
		})
	}
}

func TestCompositionContractInventoryDetached(t *testing.T) {
	schema, registry := contractRegistry(t)
	original, err := newContract(schema, registry, "test.v1")
	require.NoError(t, err)
	operations := schema.Descriptors()
	operations[0].Name = "changed"
	resources := schema.ResourceDescriptors()
	resources[0].Name = "changed"
	current, err := newContract(schema, registry, "test.v1")
	require.NoError(t, err)
	require.Equal(t, original, current)
}

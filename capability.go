package boro

import (
	"context"
	"fmt"
	"reflect"

	"github.com/pelageech/boro/scenario"
)

// Capability contributes pure declarations and typed execution bindings.
// Both methods assemble immutable contracts without acquiring resources or
// materializing invocation factories. Acquisition belongs to preparation.
type Capability interface {
	Declare(scenario.Schema) (scenario.Schema, error)
	Register(Bindings) (Bindings, error)
}

// Composition holds the declared language, executable bindings and compatibility
// identity assembled together. It owns no clients or environment lifecycle.
type Composition struct {
	schema   scenario.Schema
	bindings Bindings
	contract Contract
}

func (c Composition) Schema() scenario.Schema { return c.schema }
func (c Composition) Contract() Contract      { return c.contract }

// Validate rejects an unassembled composition before execution or image admission.
func (c Composition) Validate() error {
	return c.contract.RequireCompatible(c.contract)
}

func (c Composition) HasExecution(name string) bool { return c.bindings.HasExecution(name) }

func (c Composition) RequireExecution(s scenario.Scenario) error {
	return c.bindings.RequireExecution(s)
}

func (c Composition) ResetDomains(s scenario.Scenario) ([]string, error) {
	return c.bindings.ResetDomains(s)
}

func (c Composition) ResetSupported(domain string) bool { return c.bindings.ResetSupported(domain) }

// Prepare binds one operation for a caller-owned scope. Full scenario execution
// materializes its factories together through ResolvedScenario.PrepareInvocation.
func (c Composition) Prepare(ctx context.Context, scope *RunScope, step scenario.Step) (PreparedOperation, error) {
	return c.bindings.Prepare(ctx, scope, step)
}

// Compose extends the builtin language with explicit capabilities. All declarations
// precede runtime registration. The semantic revision remains the caller's promise
// about validators and callbacks; hashes do not establish their equivalence.
func Compose(revision string, capabilities ...Capability) (Composition, error) {
	schema := scenario.Schema{}
	for index, capability := range capabilities {
		if capability == nil || (reflect.ValueOf(capability).Kind() == reflect.Pointer && reflect.ValueOf(capability).IsNil()) {
			return Composition{}, fmt.Errorf("capabilities[%d] is nil: %w", index, scenario.ErrInput)
		}
		var err error
		schema, err = capability.Declare(schema)
		if err != nil {
			return Composition{}, fmt.Errorf("declare capability %d: %w", index, err)
		}
	}
	registry := newBindings(schema)
	for index, capability := range capabilities {
		var err error
		registry, err = capability.Register(registry)
		if err != nil {
			return Composition{}, fmt.Errorf("register capability %d: %w", index, err)
		}
	}
	contract, err := newContract(schema, registry, revision)
	if err != nil {
		return Composition{}, fmt.Errorf("compose capabilities: %w", err)
	}
	return Composition{schema: schema, bindings: registry, contract: contract}, nil
}

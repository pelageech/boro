package boro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	scen "github.com/pelageech/boro/scenario"
)

// RunnerContractLabel binds the compiled composition to immutable image config.
const RunnerContractLabel = "yc.lab.contract"

var ErrContract = errors.New("incompatible boro composition")

// Contract separates declared semantics from mechanically checked static shape.
// It does not derive or claim callback equivalence from function names or pointers.
type Contract struct {
	Version       int    `json:"version"`
	Revision      string `json:"revision"`
	SchemaSHA256  string `json:"schema_sha256"`
	RuntimeSHA256 string `json:"runtime_sha256"`
}

// newContract checks pure/runtime schema parity without calling validators,
// factories or clients, then records complete execution and reset availability.
func newContract(schema scen.Schema, registry Bindings, revision string) (Contract, error) {
	if revision == "" || strings.TrimSpace(revision) != revision {
		return Contract{}, fmt.Errorf("explicit semantic revision required: %w", ErrContract)
	}
	shape, err := schemaContractHash(schema)
	if err != nil {
		return Contract{}, err
	}
	runtimeShape, err := schemaContractHash(registry.Schema())
	if err != nil {
		return Contract{}, err
	}
	if shape != runtimeShape {
		return Contract{}, fmt.Errorf("pure and runtime schema shapes differ: %w", ErrContract)
	}
	type execution struct {
		Name      string
		Available bool
	}
	operations := make([]execution, 0, len(schema.Descriptors()))
	for _, descriptor := range schema.Descriptors() {
		operations = append(operations, execution{descriptor.Name, registry.HasExecution(descriptor.Name)})
	}
	runtimeHash, err := contractHash(struct {
		Operations []execution
		Reset      []string
	}{operations, slices.Sorted(maps.Keys(registry.resetHandlers))})
	if err != nil {
		return Contract{}, err
	}
	return Contract{Version: 1, Revision: revision, SchemaSHA256: shape, RuntimeSHA256: runtimeHash}, nil
}

// RequireCompatible admits only equal, complete contract identities.
func (c Contract) RequireCompatible(runner Contract) error {
	for _, contract := range []Contract{c, runner} {
		if contract.Version != 1 || contract.Revision == "" || strings.TrimSpace(contract.Revision) != contract.Revision {
			return fmt.Errorf("missing or unsupported contract identity: %w", ErrContract)
		}
		for _, hash := range []string{contract.SchemaSHA256, contract.RuntimeSHA256} {
			decoded, err := hex.DecodeString(hash)
			if err != nil || len(decoded) != sha256.Size {
				return fmt.Errorf("invalid contract shape hash: %w", ErrContract)
			}
		}
	}
	if c.Revision != runner.Revision {
		return fmt.Errorf("owner revision %q differs from runner %q: %w", c.Revision, runner.Revision, ErrContract)
	}
	if c.SchemaSHA256 != runner.SchemaSHA256 {
		return fmt.Errorf("owner and runner schema shapes differ: %w", ErrContract)
	}
	if c.RuntimeSHA256 != runner.RuntimeSHA256 {
		return fmt.Errorf("owner and runner execution/reset coverage differs: %w", ErrContract)
	}
	return nil
}

type contractOperation struct {
	Name, Capability, Role, ResetDomain string
	Input, Want                         contractType
	SelectorCapability                  string
	ValidateSelectors                   bool
	Validate                            bool
}

type contractResource struct {
	Name, Capability     string
	Type                 contractType
	Assets, Requirements bool
	Validate             bool
}

type contractResetCheck struct {
	Kind                    string
	Domains                 []string
	Scope, Expected, Actual contractType
}

type contractType struct {
	Name, Package, Kind string
	Reference           bool            `json:",omitempty"`
	Length              int             `json:",omitempty"`
	Element             *contractType   `json:",omitempty"`
	Key                 *contractType   `json:",omitempty"`
	Fields              []contractField `json:",omitempty"`
}

type contractField struct {
	Name, Package, Tag string
	Anonymous          bool
	Type               contractType
}

func schemaContractHash(schema scen.Schema) (string, error) {
	operations := make([]contractOperation, 0, len(schema.Descriptors()))
	for _, descriptor := range schema.Descriptors() {
		operations = append(operations, contractOperation{
			Name: descriptor.Name, Capability: descriptor.Capability, Role: descriptor.Role, ResetDomain: descriptor.ResetDomain,
			Input:              describeContractType(descriptor.Input, make(map[reflect.Type]bool)),
			Want:               describeContractType(descriptor.Want, make(map[reflect.Type]bool)),
			SelectorCapability: descriptor.SelectorCapability, ValidateSelectors: descriptor.ValidateSelectors,
			Validate: descriptor.Validate != nil,
		})
	}
	resources := make([]contractResource, 0, len(schema.ResourceDescriptors()))
	for _, descriptor := range schema.ResourceDescriptors() {
		resources = append(resources, contractResource{
			Name: descriptor.Name, Capability: descriptor.Capability,
			Type:   describeContractType(descriptor.Type, make(map[reflect.Type]bool)),
			Assets: descriptor.Assets != nil, Requirements: descriptor.Requirements != nil,
			Validate: descriptor.Validate != nil,
		})
	}
	checks := make([]contractResetCheck, 0, len(schema.ResetCheckDescriptors()))
	for _, descriptor := range schema.ResetCheckDescriptors() {
		checks = append(checks, contractResetCheck{
			Kind: descriptor.Kind, Domains: descriptor.Domains,
			Scope:    describeContractType(descriptor.Scope, make(map[reflect.Type]bool)),
			Expected: describeContractType(descriptor.Expected, make(map[reflect.Type]bool)),
			Actual:   describeContractType(descriptor.Actual, make(map[reflect.Type]bool)),
		})
	}
	validated := make([]contractType, 0, len(schema.ValidatedTypes()))
	for _, shape := range schema.ValidatedTypes() {
		validated = append(validated, describeContractType(shape, make(map[reflect.Type]bool)))
	}
	return contractHash(struct {
		Operations    []contractOperation
		Resources     []contractResource
		Formats       []string
		Validated     []contractType
		ResetChecks   []contractResetCheck
		ResetDomains  []string
		ResetProfiles []scen.ResetProfilePolicy
	}{operations, resources, schema.FormatNames(), validated, checks, schema.ResetDomainNames(), schema.ResetProfilePolicies()})
}

// Walking the concrete type graph catches same-name field/tag changes between
// builds. Recursion uses type identity, never addresses or architecture sizes.
func describeContractType(t reflect.Type, visiting map[reflect.Type]bool) contractType {
	if t == nil {
		return contractType{}
	}
	result := contractType{Name: t.Name(), Package: t.PkgPath(), Kind: t.Kind().String()}
	if visiting[t] {
		result.Reference = true
		return result
	}
	visiting[t] = true
	defer delete(visiting, t)
	switch t.Kind() {
	case reflect.Struct:
		for field := range t.Fields() {
			result.Fields = append(result.Fields, contractField{
				Name: field.Name, Package: field.PkgPath, Tag: string(field.Tag), Anonymous: field.Anonymous,
				Type: describeContractType(field.Type, visiting),
			})
		}
	case reflect.Array, reflect.Slice, reflect.Pointer, reflect.Map:
		element := describeContractType(t.Elem(), visiting)
		result.Element = &element
		if t.Kind() == reflect.Array {
			result.Length = t.Len()
		}
		if t.Kind() == reflect.Map {
			key := describeContractType(t.Key(), visiting)
			result.Key = &key
		}
	}
	return result
}

func contractHash(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode composition contract: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

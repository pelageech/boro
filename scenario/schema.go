package scenario

import (
	"maps"
	"reflect"
)

// OperationSpec declares the static contract of a compiled operation.
type OperationSpec struct {
	Name        string
	Capability  string
	Role        string
	ResetDomain string
}

const RegistryIdentity = "boro.v1"

type Descriptor struct {
	Name               string
	Input              reflect.Type
	Want               reflect.Type
	Capability         string
	Role               string
	ResetDomain        string
	SelectorCapability string
	ValidateSelectors  bool
	Validate           func(ValidationContext, Step) error
}

// Action declares an input struct and optional explicit typed validators.
func Action[I any](spec OperationSpec, validate func(ValidationContext, I) error) Descriptor {
	d := Descriptor{
		Name: spec.Name, Input: reflect.TypeFor[I](), Capability: spec.Capability, Role: spec.Role, ResetDomain: spec.ResetDomain,
		SelectorCapability: spec.Capability, ValidateSelectors: true,
	}
	if validate != nil {
		d.Validate = func(ctx ValidationContext, step Step) error {
			input, ok := step.Input.(I)
			if !ok {
				return invalid("with", "registered action payload type mismatch")
			}
			if err := validate(Clone(ctx), Clone(input)); err != nil {
				return PrefixIssue("with", err)
			}
			return nil
		}
	}
	return d
}

// Observer declares input and expectation structs with explicit typed validators.
func Observer[I, W any](spec OperationSpec, validate func(ValidationContext, I, W) error) Descriptor {
	d := Descriptor{
		Name: spec.Name, Input: reflect.TypeFor[I](), Want: reflect.TypeFor[W](), Capability: spec.Capability, Role: spec.Role, ResetDomain: spec.ResetDomain,
		SelectorCapability: spec.Capability, ValidateSelectors: true,
	}
	if validate != nil {
		d.Validate = func(ctx ValidationContext, step Step) error {
			input, inputOK := step.Input.(I)
			want, wantOK := step.Want.(W)
			if !inputOK || !wantOK {
				return invalid("with", "registered observer payload type mismatch")
			}
			return validate(Clone(ctx), Clone(input), Clone(want))
		}
	}
	return d
}

// Operation describes validation support separately from execution availability.
type Operation struct {
	Name               string `json:"name"`
	Capability         string `json:"capability"`
	ExecutionAvailable bool   `json:"execution_available"`
	ResetDomain        string `json:"reset_domain,omitempty"`
}

// Schema contains immutable language descriptors and no executable factories.
// Its zero value is an empty language with only generic scalar constraints.
type Schema struct {
	standalone     bool
	operations     map[string]Descriptor
	resources      map[string]ResourceDescriptor
	typeValidators map[reflect.Type]func(ValidationContext, any) error
	formats        map[string]func(string) error
	resetChecks    map[string]ResetCheckDescriptor
	resetDomains   map[string]ResetDomainRegistration
	resetProfiles  map[string]ResetProfilePolicy
}

func (s Schema) entries() map[string]Descriptor {
	if s.operations == nil {
		return map[string]Descriptor{}
	}
	return s.operations
}
func (s Schema) Lookup(name string) (Descriptor, bool) { d, ok := s.entries()[name]; return d, ok }

// WithDescriptor preserves the static contract of an existing operation.
func (s Schema) WithDescriptor(d Descriptor) (Schema, error) {
	if !ValidName(d.Name) || d.Capability == "" || d.Input == nil || d.Input.Kind() != reflect.Struct || (d.Want != nil && d.Want.Kind() != reflect.Struct) {
		return Schema{}, invalid("registry", "name, capability and concrete struct payloads are required")
	}
	for _, shape := range []reflect.Type{d.Input, d.Want} {
		if shape != nil {
			if err := s.validateShape(shape, make(map[reflect.Type]bool)); err != nil {
				return Schema{}, PrefixIssue("registry."+d.Name, err)
			}
		}
	}
	if previous, ok := s.Lookup(d.Name); ok && !previous.sameContract(d) {
		return Schema{}, invalid("registry", "handler changes an existing schema: "+d.Name)
	}
	entries := maps.Clone(s.entries())
	entries[d.Name] = d
	s.operations = entries
	return s, nil
}

// Spec returns the metadata used by an executable registration.
func (d Descriptor) Spec() OperationSpec {
	return OperationSpec{Name: d.Name, Capability: d.Capability, Role: d.Role, ResetDomain: d.ResetDomain}
}

func (d Descriptor) sameContract(other Descriptor) bool {
	return d.Name == other.Name && d.Input == other.Input && d.Want == other.Want &&
		d.Capability == other.Capability && d.Role == other.Role && d.ResetDomain == other.ResetDomain &&
		d.SelectorCapability == other.SelectorCapability && d.ValidateSelectors == other.ValidateSelectors
}

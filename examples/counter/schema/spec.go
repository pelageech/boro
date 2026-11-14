// Package schema defines the counter example without importing its clients.
package schema

import (
	"fmt"

	"boro/scenario"
)

const Name = "counter"

type Counter struct {
	Name string `yaml:"name" rule:"required"`
}

type IncrementInput struct {
	Counter scenario.Ref[Counter] `yaml:"counter" rule:"required"`
	By      int                   `yaml:"by" rule:"required"`
}

type ValueInput struct {
	Counter scenario.Ref[Counter] `yaml:"counter" rule:"required"`
}

type ValueWant struct {
	Value int `yaml:"value" rule:"required"`
}

func Resource() scenario.ResourceRegistration[Counter] {
	return scenario.ResourceRegistration[Counter]{
		Spec:     scenario.ResourceSpec{Name: "counter.fixture", Capability: Name},
		Validate: func(_ scenario.ValidationContext, value Counter) error { return ValidateCounter(value) },
	}
}

func ValidateCounter(value Counter) error {
	if !scenario.ValidName(value.Name) {
		return fmt.Errorf("counter name must be an identifier: %w", scenario.ErrInput)
	}
	return nil
}

// ParseYAML reads a standalone spec with the scenario loader's strict rules.
func ParseYAML(data []byte) (Counter, error) {
	spec, err := scenario.ParseResourceYAML(data, Resource())
	if err != nil {
		return Counter{}, fmt.Errorf("parse counter YAML: %w", err)
	}
	return spec, nil
}

func Increment() scenario.Descriptor {
	return scenario.Action[IncrementInput](scenario.OperationSpec{Name: "counter.increment", Capability: Name},
		func(_ scenario.ValidationContext, input IncrementInput) error { return ValidateIncrement(input) })
}

func Value() scenario.Descriptor {
	return scenario.Observer[ValueInput, ValueWant](scenario.OperationSpec{Name: "counter.value", Capability: Name},
		func(_ scenario.ValidationContext, input ValueInput, want ValueWant) error {
			return ValidateValue(input, want)
		})
}

func ValidateIncrement(input IncrementInput) error {
	if input.By <= 0 {
		return fmt.Errorf("increment must be positive: %w", scenario.ErrInput)
	}
	return nil
}

func ValidateValue(_ ValueInput, want ValueWant) error {
	if want.Value < 0 {
		return fmt.Errorf("expected counter value must be nonnegative: %w", scenario.ErrInput)
	}
	return nil
}

func Register(base scenario.Schema) (scenario.Schema, error) {
	s, err := scenario.WithResource(base, Resource())
	if err != nil {
		return scenario.Schema{}, fmt.Errorf("register counter resource: %w", err)
	}
	for _, descriptor := range []scenario.Descriptor{Increment(), Value()} {
		s, err = s.WithDescriptor(descriptor)
		if err != nil {
			return scenario.Schema{}, fmt.Errorf("register counter operation: %w", err)
		}
	}
	return s, nil
}

func Profile() scenario.ProfileDeclaration {
	return scenario.ProfileDeclaration{
		Name: Name, Capabilities: []string{Name},
		Services: []scenario.ServiceDeclaration{{Name: Name, Roles: []string{Name}, Capabilities: []string{Name}}},
	}
}

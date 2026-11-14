package scenario

import (
	"maps"
	"reflect"
	"slices"
)

// ValidationContext contains detached offline or concrete binding data.
type ValidationContext struct {
	Definition Definition
	Profile    ProfileDeclaration
}

type AssetSource struct {
	Path  string
	Field string
}

type ServiceActivation struct {
	Service    string
	Activation string
}

type ResourceRequirements struct {
	Activations []ServiceActivation
}

// WithTypeValidation registers an explicit validator for a nested data type.
func WithTypeValidation[T any](s Schema, validate func(ValidationContext, T) error) (Schema, error) {
	shape := reflect.TypeFor[T]()
	if validate == nil {
		return Schema{}, invalid("registry", "type validator is required")
	}
	if err := s.validateShape(shape, make(map[reflect.Type]bool)); err != nil {
		return Schema{}, PrefixIssue("registry", err)
	}
	validators := maps.Clone(s.validators())
	if _, exists := validators[shape]; exists {
		return Schema{}, invalid("registry", "type validator already registered: "+shape.String())
	}
	validators[shape] = func(ctx ValidationContext, value any) error {
		typed, ok := value.(T)
		if !ok {
			return invalid("", "registered nested payload type mismatch")
		}
		// The private tree dispatcher has already detached both arguments.
		return validate(ctx, typed)
	}
	s.typeValidators = validators
	return s, nil
}

func (s Schema) validators() map[reflect.Type]func(ValidationContext, any) error {
	if s.typeValidators == nil {
		return map[reflect.Type]func(ValidationContext, any) error{}
	}
	return s.typeValidators
}

// WithFormat adds a named string format without replacing an existing contract.
func (s Schema) WithFormat(name string, validate func(string) error) (Schema, error) {
	if !ValidName(name) || validate == nil {
		return Schema{}, invalid("registry", "format name and validator are required")
	}
	formats := maps.Clone(s.formatEntries())
	if _, exists := formats[name]; exists {
		return Schema{}, invalid("registry", "format already registered: "+name)
	}
	formats[name] = validate
	s.formats = formats
	return s, nil
}

func (s Schema) formatEntries() map[string]func(string) error {
	if s.formats != nil {
		return s.formats
	}
	return map[string]func(string) error{
		"prefix":  func(value string) error { return checkFormat("prefix", value) },
		formatMAC: func(value string) error { return checkFormat(formatMAC, value) },
	}
}

// Requirements enumerates detached resource requirements in resource-name order.
// ValidateDefinition checks their service permissions against the actual profile.
func (s Schema) Requirements(definition Definition) (ResourceRequirements, error) {
	out := ResourceRequirements{}
	owners := make(map[string]Name)
	for _, name := range slices.Sorted(maps.Keys(definition.Resources)) {
		resource := definition.Resources[name]
		descriptor, ok := s.LookupResource(resource.Type)
		if !ok || reflect.TypeOf(resource.Spec) != descriptor.Type {
			return ResourceRequirements{}, invalid("resources."+string(name), "unknown or mismatched resource type")
		}
		if descriptor.Requirements == nil {
			continue
		}
		requirements, err := descriptor.Requirements(Clone(resource.Spec))
		if err != nil {
			return ResourceRequirements{}, PrefixIssue("resources."+string(name)+".spec", err)
		}
		for _, activation := range requirements.Activations {
			if activation.Service == "" || activation.Activation == "" {
				return ResourceRequirements{}, invalid("resources."+string(name), "service and activation are required")
			}
			if owner, exists := owners[activation.Service]; exists {
				return ResourceRequirements{}, invalid("resources."+string(name), "activation service already reserved by "+string(owner))
			}
			owners[activation.Service] = name
			out.Activations = append(out.Activations, activation)
		}
	}
	return Clone(out), nil
}

func (s Schema) validateRequirements(definition Definition, profile ProfileDeclaration) error {
	requirements, err := s.Requirements(definition)
	if err != nil {
		return err
	}
	for _, activation := range requirements.Activations {
		services, err := profile.SelectServices(Selector{Service: activation.Service}, "")
		if err != nil {
			return PrefixIssue("resources", err)
		}
		if len(services) != 1 || services[0].Activation != activation.Activation {
			return invalid("resources", "service does not permit activation "+activation.Activation)
		}
	}
	return nil
}

// LookupFormat exposes a registered constraint for declaration compatibility checks.
func (s Schema) LookupFormat(name string) (func(string) error, bool) {
	validate, ok := s.formatEntries()[name]
	return validate, ok
}

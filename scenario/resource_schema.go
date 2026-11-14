package scenario

import (
	"maps"
	"reflect"
)

// ResourceSpec declares the stable name and required profile capability of a resource.
type ResourceSpec struct {
	Name       string
	Capability string
}

// ResourceRegistration supplies a concrete data struct and an explicit validator.
// Validate runs during loading and binding, before infrastructure acquisition.
type ResourceRegistration[T any] struct {
	Spec         ResourceSpec
	Validate     func(ValidationContext, T) error
	Assets       func(T) ([]AssetSource, error)
	Requirements func(T) (ResourceRequirements, error)
}

// ResourceDescriptor is the immutable decoding contract of a resource kind.
type ResourceDescriptor struct {
	ResourceSpec

	Type         reflect.Type
	Validate     func(ValidationContext, any) error
	Assets       func(any) ([]AssetSource, error)
	Requirements func(any) (ResourceRequirements, error)
}

func (s Schema) resourceEntries() map[string]ResourceDescriptor {
	if s.resources == nil {
		return map[string]ResourceDescriptor{}
	}
	return s.resources
}

// LookupResource returns the static contract for a resource kind.
func (s Schema) LookupResource(name string) (ResourceDescriptor, bool) {
	d, ok := s.resourceEntries()[name]
	return d, ok
}

// WithResource returns a schema with a typed resource registration.
// Register resource kinds before operations that refer to them.
func WithResource[T any](s Schema, entry ResourceRegistration[T]) (Schema, error) {
	d := ResourceDescriptor{ResourceSpec: entry.Spec, Type: reflect.TypeFor[T]()}
	if entry.Validate != nil {
		d.Validate = func(ctx ValidationContext, value any) error {
			typed, ok := value.(T)
			if !ok {
				return invalid("", "registered resource payload type mismatch")
			}
			return entry.Validate(Clone(ctx), Clone(typed))
		}
	}
	if entry.Assets != nil {
		d.Assets = func(value any) ([]AssetSource, error) {
			typed, ok := value.(T)
			if !ok {
				return nil, invalid("", "registered resource payload type mismatch")
			}
			assets, err := entry.Assets(Clone(typed))
			return Clone(assets), err
		}
	}
	if entry.Requirements != nil {
		d.Requirements = func(value any) (ResourceRequirements, error) {
			typed, ok := value.(T)
			if !ok {
				return ResourceRequirements{}, invalid("", "registered resource payload type mismatch")
			}
			requirements, err := entry.Requirements(Clone(typed))
			return Clone(requirements), err
		}
	}
	return s.WithResourceDescriptor(d)
}

// WithResourceDescriptor preserves existing resource contracts and validates new shapes.
func (s Schema) WithResourceDescriptor(d ResourceDescriptor) (Schema, error) {
	if !ValidName(d.Name) || d.Capability == "" || d.Type == nil || d.Type.Kind() != reflect.Struct {
		return Schema{}, invalid("registry", "resource name, capability and concrete struct payload are required")
	}
	if previous, ok := s.LookupResource(d.Name); ok && !previous.sameContract(d) {
		return Schema{}, invalid("registry", "registration changes an existing resource schema: "+d.Name)
	}
	entries := maps.Clone(s.resourceEntries())
	for name, entry := range entries {
		if name != d.Name && entry.Type == d.Type {
			return Schema{}, invalid("registry", "resource struct already registered as "+name)
		}
	}
	entries[d.Name] = d
	s.resources = entries
	if err := s.validateShape(d.Type, make(map[reflect.Type]bool)); err != nil {
		return Schema{}, PrefixIssue("registry."+d.Name, err)
	}
	return s, nil
}

func (d ResourceDescriptor) sameContract(other ResourceDescriptor) bool {
	return d.ResourceSpec == other.ResourceSpec && d.Type == other.Type &&
		(d.Assets == nil) == (other.Assets == nil) && (d.Requirements == nil) == (other.Requirements == nil)
}

// Ref names a scenario-local resource with the registered concrete payload T.
// It is represented by a plain string in YAML and JSON.
type Ref[T any] string

func (Ref[T]) resourceType() reflect.Type { return reflect.TypeFor[T]() }

type resourceReference interface {
	resourceType() reflect.Type
}

// ResolveResource returns a detached typed fixture for a reference.
func ResolveResource[T any](definition Definition, ref Ref[T]) (T, error) {
	resource, exists := definition.Resources[Name(ref)]
	value, ok := resource.Spec.(T)
	if !exists || !ok {
		var zero T
		return zero, invalid("resources."+string(ref), "resource reference has missing or mismatched payload")
	}
	return Clone(value), nil
}

func (s Schema) resourceName(t reflect.Type) string {
	for name, entry := range s.resourceEntries() {
		if entry.Type == t {
			return name
		}
	}
	return ""
}

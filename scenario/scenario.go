package scenario

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// Name identifies a scenario-local resource.
type Name string

// Payload holds a registered concrete resource, operation input, or expectation.
// Schema registration checks the concrete data shape before decoding.
type Payload = any

// Environment selects a statically declared lab profile.
type Environment struct {
	// chooseProfile checks this name against the explicitly supplied declarations.
	Profile     string `yaml:"profile" rule:"required"`
	AllowAttach bool   `yaml:"allow_attach"`
}

// Timeouts bounds the scenario, operations, observation calls, and sampling.
type Timeouts struct {
	Scenario    Duration `yaml:"scenario" rule:"required"`
	Action      Duration `yaml:"action" rule:"required"`
	Observation Duration `yaml:"observation" rule:"required"`
	Poll        Duration `yaml:"poll" rule:"required"`
}

// Duration is a positive Go duration parsed at the YAML boundary.
type Duration time.Duration

// Selector names one service or a nonempty role selection.
type Selector struct {
	Service string `yaml:"service"`
	Role    string `yaml:"role"`
	Match   string `yaml:"match" enum:"all|first"`
}

// Address is either a literal IP or an explicitly typed environment reference.
type Address struct {
	Literal string `json:"literal,omitempty"`
	Env     string `json:"env,omitempty"`
}

// Resource is a strictly decoded immutable fixture in a scenario snapshot.
type Resource struct {
	Type string
	Spec Payload
}

// Wait declares exactly one observation timing policy.
type Wait struct {
	Mode     string    `yaml:"mode" rule:"required" enum:"once|eventually|remains"`
	Timeout  *Duration `yaml:"timeout"`
	Duration *Duration `yaml:"duration"`
	Poll     *Duration `yaml:"poll"`
}

// Step is a typed action, observation, or parallel block.
type Step struct {
	ID        string
	Operation string
	Input     Payload
	Want      Payload
	Wait      *Wait
	Timeout   *Duration
	Branches  []Branch
}

// Branch contains ordered steps under a shared parallel deadline.
type Branch struct {
	ID    string
	Steps []Step
}

// Definition is an isolated snapshot; changing it never changes a Scenario.
type Definition struct {
	Version     int
	ID          string
	Description string
	Tags        []string
	Environment Environment
	Timeouts    Timeouts
	Resources   map[Name]Resource
	Setup       []Step
	Steps       []Step
	Teardown    []Step
}

// Scenario holds parsed input and captured asset bytes without mutable aliases.
type Scenario struct {
	definition  Definition
	source      string
	sourceHash  string
	hash        string
	assets      []Asset
	profile     ProfileDeclaration
	branchLimit int
	schema      Schema
}

// ID returns the stable case identity.
func (s Scenario) ID() string { return s.definition.ID }

// Hash includes the scenario bytes and every referenced asset's content.
func (s Scenario) Hash() string { return s.hash }

// Source returns the selected YAML path for immutable invocation capture.
func (s Scenario) Source() string { return s.source }

// Snapshot returns a detached, typed copy for consumers.
func (s Scenario) Snapshot() Definition {
	return Clone(s.definition)
}

// Assets returns captured fixture contents, never rereading mutable paths.
func (s Scenario) Assets() []Asset {
	return Clone(s.assets)
}

// MarshalJSON exposes only the detached scenario definition.
func (s Scenario) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(s.definition)
	if err != nil {
		return nil, fmt.Errorf("encode scenario snapshot: %w", err)
	}
	return data, nil
}

// Clone copies the data-only trees used by scenario definitions.
func Clone[T any](value T) T {
	var out T
	reflect.ValueOf(&out).Elem().Set(cloneValue(reflect.ValueOf(value)))
	return out
}

func cloneValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(cloneValue(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(cloneValue(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(cloneValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		for iter := v.MapRange(); iter.Next(); {
			out.SetMapIndex(iter.Key(), cloneValue(iter.Value()))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := range v.NumField() {
			if out.Field(i).CanSet() && v.Type().Field(i).IsExported() {
				out.Field(i).Set(cloneValue(v.Field(i)))
			}
		}
		return out
	default:
		return v
	}
}

// Profile returns detached declarations used to bind this document.
func (s Scenario) Profile() ProfileDeclaration { return Clone(s.profile) }

// Schema returns this document's immutable static descriptors.
func (s Scenario) Schema() Schema { return s.schema }

// ValidateBinding checks a concrete definition against this document and the supplied schema.
func (s Scenario) ValidateBinding(definition Definition, profile ProfileDeclaration, schema Schema) error {
	for _, resource := range definition.Resources {
		original, _ := s.schema.LookupResource(resource.Type)
		actual, ok := schema.LookupResource(resource.Type)
		if !ok || !original.sameContract(actual) {
			return invalid("registry", "binding changes an existing resource schema: "+resource.Type)
		}
	}
	names, _ := Operations(definition)
	for _, name := range names {
		original, _ := s.schema.Lookup(name)
		actual, ok := schema.Lookup(name)
		if !ok || !original.sameContract(actual) {
			return invalid("registry", "binding changes an existing schema: "+name)
		}
	}
	return schema.ValidateDefinition(definition, profile, s.branchLimit)
}

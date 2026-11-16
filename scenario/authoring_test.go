package scenario_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	scen "github.com/pelageech/boro/scenario"
)

type counterFixture struct {
	Start   int                    `yaml:"start" rule:"required"`
	History []int                  `yaml:"history"`
	Link    scen.Ref[labelFixture] `yaml:"link"`
}

type labelFixture struct {
	Label string `yaml:"label" rule:"required"`
}

type counterInput struct {
	Counter  scen.Ref[counterFixture]            `yaml:"counter" rule:"required"`
	Counters []scen.Ref[counterFixture]          `yaml:"counters"`
	Named    map[string]scen.Ref[counterFixture] `yaml:"named"`
	Delta    int                                 `yaml:"delta"`
}

type counterWant struct {
	Value int `yaml:"value" rule:"required"`
}

// An extension's methods are never implicitly selected as schema validators.
func (counterWant) Validate() error { return errors.New("implicit validator was called") }

const counterDocument = `version: 1
id: example.counter
environment: {profile: nlb}
timeouts: {scenario: 5s, action: 1s, observation: 1s, poll: 10ms}
resources:
  counter:
    type: example.counter
    spec: {start: 2, history: [2], link: other}
  other:
    type: example.label
    spec: {label: other}
setup:
  - id: increment
    do: example.increment
    with: {counter: counter, delta: 1}
steps:
  - id: observe
    expect: example.value
    with: {counter: counter, counters: [counter], named: {primary: counter}}
    want: {value: 3}
    wait: {mode: once}
`

func counterSchema(t *testing.T) scen.Schema {
	t.Helper()
	schema, err := scen.WithResource(scen.Schema{}, scen.ResourceRegistration[labelFixture]{
		Spec: scen.ResourceSpec{Name: "example.label", Capability: "counter"},
	})
	require.NoError(t, err)
	schema, err = scen.WithResource(schema, scen.ResourceRegistration[counterFixture]{
		Spec: scen.ResourceSpec{Name: "example.counter", Capability: "counter"},
		Validate: func(_ scen.ValidationContext, value counterFixture) error {
			if value.Start < 0 {
				return errors.New("start must be nonnegative")
			}
			if len(value.History) > 0 {
				value.History[0] = 99
			}
			return nil
		},
	})
	require.NoError(t, err)
	action := scen.Action[counterInput](scen.OperationSpec{Capability: "counter"}, func(_ scen.ValidationContext, input counterInput) error {
		if input.Delta <= 0 {
			return errors.New("delta must be positive")
		}
		return nil
	})
	action.Name = "example.increment"
	schema, err = schema.WithDescriptor(action)
	require.NoError(t, err)
	observer := scen.Observer[counterInput, counterWant](scen.OperationSpec{Capability: "counter"}, func(_ scen.ValidationContext, input counterInput, want counterWant) error {
		if want.Value < 0 {
			return errors.New("value must be nonnegative")
		}
		input.Named["primary"] = "changed-by-validator"
		return nil
	})
	observer.Name = "example.value"
	schema, err = schema.WithDescriptor(observer)
	require.NoError(t, err)
	return schema
}

func loadCounter(t *testing.T, schema scen.Schema, document string) (scen.Scenario, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "counter.yaml")
	require.NoError(t, os.WriteFile(path, []byte(document), 0o600))
	return scen.LoadScenario(t.Context(), path, scen.LoadOptions{
		Schema: schema,
		Profiles: []scen.ProfileDeclaration{{
			Name: "nlb", Capabilities: []string{"counter"},
		}},
	})
}

func TestOrdinaryPayloadsAndTypedResources(t *testing.T) {
	schema := counterSchema(t)
	scenario, err := loadCounter(t, schema, counterDocument)
	require.NoError(t, err)
	snapshot := scenario.Snapshot()
	input, ok := snapshot.Steps[0].Input.(counterInput)
	require.True(t, ok)
	require.Equal(t, scen.Ref[counterFixture]("counter"), input.Named["primary"])
	fixture, err := scen.ResolveResource(snapshot, input.Counter)
	require.NoError(t, err)
	require.Equal(t, []int{2}, fixture.History, "validators receive detached values")
	label, err := scen.ResolveResource(snapshot, fixture.Link)
	require.NoError(t, err)
	require.Equal(t, "other", label.Label)
	fixture.History[0] = 42
	again, err := scen.ResolveResource(snapshot, input.Counter)
	require.NoError(t, err)
	require.Equal(t, []int{2}, again.History)
	input.Named["primary"] = "changed-by-consumer"
	next, ok := scenario.Snapshot().Steps[0].Input.(counterInput)
	require.True(t, ok)
	require.Equal(t, scen.Ref[counterFixture]("counter"), next.Named["primary"])
	setup, ok := snapshot.Setup[0].Input.(counterInput)
	require.True(t, ok)
	require.Nil(t, setup.Named, "cloning preserves omitted maps")
	data, err := json.Marshal(input.Counter)
	require.NoError(t, err)
	require.JSONEq(t, `"counter"`, string(data))
	require.NoError(t, scenario.ValidateBinding(scenario.Snapshot(), scenario.Profile(), schema))
	require.ErrorContains(t, scenario.ValidateBinding(scenario.Snapshot(), scenario.Profile(), scen.Schema{}), "resource schema")
	_, err = scen.ResolveResource(snapshot, scen.Ref[labelFixture]("counter"))
	require.ErrorContains(t, err, "mismatched payload")
	_, err = scen.ResolveResource(snapshot, scen.Ref[counterFixture]("missing"))
	require.ErrorContains(t, err, "missing")

	t.Run("binding checks exact resource payload type", func(t *testing.T) {
		snapshot := scenario.Snapshot()
		snapshot.Resources["counter"] = scen.Resource{Type: "example.counter", Spec: labelFixture{Label: "wrong"}}
		require.ErrorContains(t, schema.ValidateDefinition(snapshot, scenario.Profile(), 2), "mismatched resource")
	})
	t.Run("binding checks exact operation payload type", func(t *testing.T) {
		snapshot := scenario.Snapshot()
		snapshot.Steps[0].Input = labelFixture{Label: "wrong"}
		require.ErrorContains(t, schema.ValidateDefinition(snapshot, scenario.Profile(), 2), "payload type mismatch")
	})
	t.Run("resource schema is immutable", func(t *testing.T) {
		_, ok := (scen.Schema{}).LookupResource("example.counter")
		require.False(t, ok)
		_, err := scen.WithResource(schema, scen.ResourceRegistration[labelFixture]{
			Spec: scen.ResourceSpec{Name: "example.counter", Capability: "counter"},
		})
		require.ErrorContains(t, err, "changes an existing resource schema")
		_, err = scen.WithResource(schema, scen.ResourceRegistration[counterFixture]{
			Spec: scen.ResourceSpec{Name: "example.alias", Capability: "counter"},
		})
		require.ErrorContains(t, err, "already registered")
	})
}

func TestExtensionInputRejectedBeforeAcquisition(t *testing.T) {
	recordSideEffects(t)
	schema := counterSchema(t)
	for _, tc := range []struct {
		name, before, after, diagnostic string
	}{
		{"unknown resource field", "start: 2", "start: 2, unknown: true", "unknown field"},
		{"unknown input field", "delta: 1", "delta: 1, unknown: true", "unknown field"},
		{"unknown want field", "value: 3", "value: 3, unknown: true", "unknown field"},
		{"missing required field", "start: 2, ", "", "required field"},
		{"wrong scalar", "start: 2", "start: two", "expected int"},
		{"invalid resource", "start: 2", "start: -1", "start must be nonnegative"},
		{"invalid action", "delta: 1", "delta: -1", "delta must be positive"},
		{"invalid observer", "value: 3", "value: -1", "value must be nonnegative"},
		{"unknown reference", "counter: counter, delta", "counter: missing, delta", "expected resource of type example.counter"},
		{"wrong reference type", "counter: counter, delta", "counter: other, delta", "expected resource of type example.counter"},
		{"wrong resource reference", "link: other", "link: counter", "expected resource of type example.label"},
		{"unknown list reference", "counters: [counter]", "counters: [missing]", "with.counters[0]"},
		{"empty list reference", "counters: [counter]", `counters: [""]`, "with.counters[0]"},
		{"unknown map reference", "primary: counter", "primary: missing", "with.named.primary"},
		{"empty map reference", "primary: counter", `primary: ""`, "with.named.primary"},
		{"unknown nested map payload", "history: [2]", "history: [{value: 2}]", "expected int"},
		{"duplicate key", "start: 2", "start: 2, start: 3", "duplicate key"},
		{"alias", "history: [2]", "history: &history [2]", "anchors and aliases"},
		{"null", "history: [2]", "history: null", "null is forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadCounter(t, schema, strings.Replace(counterDocument, tc.before, tc.after, 1))
			require.ErrorContains(t, err, tc.diagnostic)
			var detail *scen.InputError
			require.ErrorAs(t, err, &detail)
			require.Positive(t, detail.Line)
			require.NotEmpty(t, detail.Field)
		})
	}
	t.Run("missing resource capability", func(t *testing.T) {
		scenario, err := loadCounter(t, schema, counterDocument)
		require.NoError(t, err)
		require.ErrorContains(t, schema.ValidateDefinition(scenario.Snapshot(), scen.ProfileDeclaration{}, 2), "profile lacks capability counter")
	})
}

type recursivePayload struct {
	Next *recursivePayload `yaml:"next"`
}

type customPayload struct {
	Value string `yaml:"value"`
}

func (*customPayload) UnmarshalYAML(*yaml.Node) error { return errors.New("must not decode") }

type legacyPayload struct {
	Value string `yaml:"value"`
}

func (*legacyPayload) UnmarshalYAML(func(any) error) error { return errors.New("must not decode") }

type textPayload struct {
	Value string `yaml:"value"`
}

func (*textPayload) UnmarshalText([]byte) error { return errors.New("must not decode") }

func TestRegistrationRejectsUnsupportedShapes(t *testing.T) {
	recordSideEffects(t)
	for _, tc := range []struct {
		name  string
		shape reflect.Type
	}{
		{"pointer root", reflect.TypeFor[*counterWant]()},
		{"interface root", reflect.TypeFor[any]()},
		{"scalar root", reflect.TypeFor[string]()},
		{"dynamic field", reflect.TypeFor[struct{ Value any }]()},
		{"function field", reflect.TypeFor[struct{ Value func() }]()},
		{"channel field", reflect.TypeFor[struct{ Value chan int }]()},
		{"hidden state", reflect.TypeFor[struct{ value string }]()},
		{"map key", reflect.TypeFor[struct{ Value map[int]string }]()},
		{"reference map key", reflect.TypeFor[struct {
			Value map[scen.Ref[labelFixture]]string
		}]()},
		{"recursive", reflect.TypeFor[recursivePayload]()},
		{"custom decoder", reflect.TypeFor[customPayload]()},
		{"legacy custom decoder", reflect.TypeFor[legacyPayload]()},
		{"text decoder", reflect.TypeFor[textPayload]()},
		{"inline map", reflect.TypeFor[struct {
			Value map[string]string `yaml:",inline"`
		}]()},
		{"duplicate fields", reflect.TypeFor[struct {
			A int `yaml:"same"`
			B int `yaml:"same"`
		}]()},
		{"invalid bound", reflect.TypeFor[struct {
			Value int `yaml:"value" min:"invalid"`
		}]()},
		{"unknown format", reflect.TypeFor[struct {
			Value string `yaml:"value" format:"unknown"`
		}]()},
		{"format on integer", reflect.TypeFor[struct {
			Value uint32 `yaml:"value" format:"prefix"`
		}]()},
		{"bound on string", reflect.TypeFor[struct {
			Value string `yaml:"value" min:"1"`
		}]()},
		{"unknown rule", reflect.TypeFor[struct {
			Value string `yaml:"value" rule:"unknown"`
		}]()},
		{"unregistered typed reference", reflect.TypeFor[counterInput]()},
		{"unknown tagged reference", reflect.TypeFor[struct {
			Value string `yaml:"value" ref:"unknown"`
		}]()},
		{"nonstring reference", reflect.TypeFor[struct {
			Value int `yaml:"value" ref:"example.label"`
		}]()},
		{"conflicting typed reference", reflect.TypeFor[struct {
			Value scen.Ref[counterFixture] `yaml:"value" ref:"example.label"`
		}]()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := scen.Schema{}
			if tc.name == "nonstring reference" || tc.name == "conflicting typed reference" {
				schema = counterSchema(t)
			}
			_, err := schema.WithDescriptor(scen.Descriptor{
				Name: "example.invalid", Capability: "counter", Input: tc.shape,
			})
			require.Error(t, err)
			_, err = schema.WithDescriptor(scen.Descriptor{
				Name: "example.invalid", Capability: "counter", Input: reflect.TypeFor[counterWant](), Want: tc.shape,
			})
			require.Error(t, err)
			_, err = schema.WithResourceDescriptor(scen.ResourceDescriptor{
				ResourceSpec: scen.ResourceSpec{Name: "example.invalid", Capability: "counter"}, Type: tc.shape,
			})
			require.Error(t, err)
		})
	}
}

func TestOrdinaryDataShapes(t *testing.T) {
	type data struct {
		Value  float64
		Hidden string `yaml:"-,omitempty"`
		Rows   map[string]struct {
			Count *uint16 `yaml:"count"`
		} `yaml:"rows"`
	}
	descriptor := scen.Observer[data, counterWant](scen.OperationSpec{Capability: "counter"}, nil)
	descriptor.Name = "example.value"
	schema, err := (scen.Schema{}).WithDescriptor(descriptor)
	require.NoError(t, err)
	const document = `version: 1
id: example.data
environment: {profile: nlb}
timeouts: {scenario: 5s, action: 1s, observation: 1s, poll: 10ms}
resources: {}
steps:
  - id: observe
    expect: example.value
    with: {value: 1.5, rows: {first: {count: 2}}}
    want: {value: 3}
    wait: {mode: once}
`
	scenario, err := loadCounter(t, schema, document)
	require.NoError(t, err)
	value, ok := scenario.Snapshot().Steps[0].Input.(data)
	require.True(t, ok)
	require.InDelta(t, 1.5, value.Value, 0.001)
	require.Equal(t, uint16(2), *value.Rows["first"].Count)
	for _, tc := range []struct{ before, after, diagnostic string }{
		{"count: 2", "count: 2, unknown: true", "unknown field"},
		{"count: 2", "count: 65536", "scalar type"},
		{"value: 1.5", `"~": ignored, value: 1.5`, "unknown field"},
		{"value: 1.5", `"-": ignored, value: 1.5`, "unknown field"},
		{"value: 1.5", "value: .nan", "finite float"},
		{"value: 1.5", "value: .inf", "finite float"},
	} {
		t.Run(tc.after, func(t *testing.T) {
			_, err := loadCounter(t, schema, strings.Replace(document, tc.before, tc.after, 1))
			require.ErrorContains(t, err, tc.diagnostic)
		})
	}
}

type contextualNested struct {
	Code   string `yaml:"code" rule:"required" format:"extension.code"`
	Values []int  `yaml:"values" rule:"required,nonempty"`
}

type contextualResource struct {
	Service string           `yaml:"service" rule:"required" service:"true"`
	Address scen.Address     `yaml:"address" rule:"required"`
	Nested  contextualNested `yaml:"nested" rule:"required"`
	Files   []string         `yaml:"files" rule:"required,nonempty"`
}

type contextualInput struct {
	Resource scen.Ref[contextualResource] `yaml:"resource" rule:"required"`
	On       scen.Selector                `yaml:"on" rule:"required"`
	Count    uint16                       `yaml:"count" rule:"required" min:"1" max:"9"`
	Enabled  bool                         `yaml:"enabled" rule:"required"`
	Policy   *contextualPolicy            `yaml:"policy"`
}

type contextualPolicy struct {
	Level uint32 `yaml:"level" rule:"required"`
}

type contextualWant struct {
	Value int `yaml:"value" rule:"required"`
}

const contextualDocument = `version: 1
id: extension.context
environment: {profile: extension}
timeouts: {scenario: 5s, action: 1s, observation: 1s, poll: 1ms}
resources:
  fixture:
    type: extension.fixture
    spec:
      service: reserved
      address: {env: addresses.test}
      nested: {code: accepted, values: [7]}
      files: [b.txt, a.txt]
setup:
  - {id: action, do: extension.action, with: {resource: fixture, on: {service: reserved}, count: 1, enabled: false}}
steps:
  - {id: observer, expect: extension.observe, with: {resource: fixture, on: {service: reserved}, count: 1, enabled: false},
     want: {value: 7}, wait: {mode: once}}
`

func contextualSchema(t *testing.T, calls map[string]int) scen.Schema {
	t.Helper()
	schema, err := (scen.Schema{}).WithFormat("extension.code", func(value string) error {
		if value != "accepted" {
			return errors.New("unrecognized extension code")
		}
		return nil
	})
	require.NoError(t, err)
	schema, err = scen.WithTypeValidation(schema, func(ctx scen.ValidationContext, value contextualNested) error {
		calls["nested"]++
		if ctx.Profile.Name != "extension" || value.Values[0] != 7 {
			return errors.New("nested profile relationship rejected")
		}
		value.Values[0] = 99
		ctx.Profile.Addresses["test"] = 6
		return nil
	})
	require.NoError(t, err)
	schema, err = scen.WithResource(schema, scen.ResourceRegistration[contextualResource]{
		Spec: scen.ResourceSpec{Name: "extension.fixture", Capability: "extension"},
		Validate: func(ctx scen.ValidationContext, value contextualResource) error {
			calls["resource"]++
			family, err := ctx.Profile.AddressFamily(value.Address)
			if err != nil || family != 4 || ctx.Definition.ID != "extension.context" {
				return errors.New("resource profile relationship rejected")
			}
			value.Nested.Values[0] = 99
			delete(ctx.Definition.Resources, "fixture")
			ctx.Profile.Services[0].Capabilities[0] = "mutated"
			return nil
		},
		Assets: func(value contextualResource) ([]scen.AssetSource, error) {
			sources := make([]scen.AssetSource, 0, len(value.Files))
			for _, path := range value.Files {
				sources = append(sources, scen.AssetSource{Path: path, Field: "files"})
			}
			value.Files[0] = "changed"
			return sources, nil
		},
		Requirements: func(value contextualResource) (scen.ResourceRequirements, error) {
			value.Nested.Values[0] = 99
			return scen.ResourceRequirements{Activations: []scen.ServiceActivation{{Service: value.Service, Activation: "extension.activate"}}}, nil
		},
	})
	require.NoError(t, err)
	action := scen.Action[contextualInput](scen.OperationSpec{Name: "extension.action", Capability: "extension"},
		func(ctx scen.ValidationContext, input contextualInput) error {
			calls["action"]++
			resource, err := scen.ResolveResource(ctx.Definition, input.Resource)
			if err != nil {
				return err
			}
			if resource.Nested.Values[0] != 7 || ctx.Profile.Addresses["test"] != 4 {
				return errors.New("action context was mutated")
			}
			delete(ctx.Definition.Resources, "fixture")
			return nil
		})
	schema, err = schema.WithDescriptor(action)
	require.NoError(t, err)
	observer := scen.Observer[contextualInput, contextualWant](scen.OperationSpec{Name: "extension.observe", Capability: "extension"},
		func(ctx scen.ValidationContext, input contextualInput, want contextualWant) error {
			calls["observer"]++
			resource, err := scen.ResolveResource(ctx.Definition, input.Resource)
			if err != nil {
				return err
			}
			if resource.Nested.Values[0] != want.Value {
				return errors.New("observer context relationship rejected")
			}
			ctx.Profile.Services[0].Capabilities[0] = "mutated"
			return nil
		})
	schema, err = schema.WithDescriptor(observer)
	require.NoError(t, err)
	return schema
}

func loadContextual(t *testing.T, schema scen.Schema, document string) (scen.Scenario, error) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("opaque: "+name), 0o600))
	}
	path := filepath.Join(root, "scenario.yaml")
	require.NoError(t, os.WriteFile(path, []byte(document), 0o600))
	return scen.LoadScenario(t.Context(), path, scen.LoadOptions{
		Schema: schema, FixtureRoot: root,
		Profiles: []scen.ProfileDeclaration{{
			Name: "extension", Capabilities: []string{"extension"}, Addresses: map[string]int{"test": 4},
			Services: []scen.ServiceDeclaration{{Name: "reserved", Capabilities: []string{"extension"}, Activation: "extension.activate"}},
		}},
	})
}

func TestContextualRegistrations(t *testing.T) {
	calls := make(map[string]int)
	schema := contextualSchema(t, calls)
	document, err := loadContextual(t, schema, contextualDocument)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"nested": 1, "resource": 1, "action": 1, "observer": 1}, calls)
	assets := document.Assets()
	require.Equal(t, []string{"a.txt", "b.txt"}, []string{assets[0].Path, assets[1].Path})
	require.Equal(t, []byte("opaque: a.txt"), assets[0].Contents)
	require.Equal(t, scen.Digest(assets[0].Contents), assets[0].SHA256)
	requirements, err := schema.Requirements(document.Snapshot())
	require.NoError(t, err)
	require.Equal(t, []scen.ServiceActivation{{Service: "reserved", Activation: "extension.activate"}}, requirements.Activations)

	concrete := document.Snapshot()
	fixture, err := scen.ResolveResource(concrete, scen.Ref[contextualResource]("fixture"))
	require.NoError(t, err)
	fixture.Address = scen.Address{Literal: "192.0.2.7"}
	concrete.Resources["fixture"] = scen.Resource{Type: "extension.fixture", Spec: fixture}
	require.NoError(t, document.ValidateBinding(concrete, document.Profile(), schema))
	require.Equal(t, map[string]int{"nested": 2, "resource": 2, "action": 2, "observer": 2}, calls)
	t.Run("pointer and value validators retain exact type semantics", func(t *testing.T) {
		for _, tc := range []struct {
			name                   string
			wantPointer, wantValue int
		}{
			{"pointer only", 1, 0},
			{"value only", 0, 1},
			{"pointer and value", 1, 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				schema := contextualSchema(t, make(map[string]int))
				pointerCalls, valueCalls := 0, 0
				var err error
				if tc.wantPointer != 0 {
					schema, err = scen.WithTypeValidation(schema, func(ctx scen.ValidationContext, policy *contextualPolicy) error {
						require.NotNil(t, policy, "omitted pointers must not invoke validators")
						require.EqualValues(t, 1, policy.Level)
						pointerCalls++
						policy.Level = 99
						ctx.Profile.Addresses["test"] = 6
						delete(ctx.Definition.Resources, "fixture")
						return nil
					})
					require.NoError(t, err)
				}
				if tc.wantValue != 0 {
					schema, err = scen.WithTypeValidation(schema, func(_ scen.ValidationContext, policy contextualPolicy) error {
						require.EqualValues(t, 1, policy.Level, "pointer callback mutation must not reach value validation")
						valueCalls++
						return nil
					})
					require.NoError(t, err)
				}
				wire := strings.Replace(contextualDocument, "count: 1", "policy: {level: 1}, count: 1", 1)
				loaded, err := loadContextual(t, schema, wire)
				require.NoError(t, err)
				require.Equal(t, tc.wantPointer, pointerCalls)
				require.Equal(t, tc.wantValue, valueCalls)
				native, profile := loaded.Snapshot(), loaded.Profile()
				require.NoError(t, loaded.ValidateBinding(native, profile, schema))
				require.Equal(t, 2*tc.wantPointer, pointerCalls)
				require.Equal(t, 2*tc.wantValue, valueCalls)
				require.Equal(t, loaded.Snapshot(), native)
				require.Equal(t, loaded.Profile(), profile)
				input, ok := native.Setup[0].Input.(contextualInput)
				require.True(t, ok)
				require.EqualValues(t, 1, input.Policy.Level)

				omitted, err := loadContextual(t, schema, contextualDocument)
				require.NoError(t, err)
				require.NoError(t, omitted.ValidateBinding(omitted.Snapshot(), omitted.Profile(), schema))
				require.Equal(t, 2*tc.wantPointer, pointerCalls)
				require.Equal(t, 2*tc.wantValue, valueCalls)
				input, ok = omitted.Snapshot().Setup[0].Input.(contextualInput)
				require.True(t, ok)
				require.Nil(t, input.Policy)
			})
		}
	})
	t.Run("standalone decoding supplies empty context without resolving references", func(t *testing.T) {
		type standalone struct {
			Address scen.Address           `yaml:"address" rule:"required"`
			Route   scen.Ref[labelFixture] `yaml:"route" rule:"required"`
			Values  []int                  `yaml:"values" rule:"required,nonempty"`
		}
		entry := scen.ResourceRegistration[standalone]{
			Spec: scen.ResourceSpec{Name: "extension.standalone", Capability: "extension"},
			Validate: func(ctx scen.ValidationContext, value standalone) error {
				require.Empty(t, ctx.Profile.Name)
				require.Empty(t, ctx.Definition.Resources)
				value.Values[0] = 99
				return nil
			},
		}
		data := []byte("address: {env: addresses.later}\nroute: later\nvalues: [7]\n")
		value, err := scen.ParseResourceYAML(data, entry)
		require.NoError(t, err)
		require.Equal(t, scen.Address{Env: "addresses.later"}, value.Address)
		require.Equal(t, []int{7}, value.Values)
		entry.Validate = func(ctx scen.ValidationContext, _ standalone) error {
			if ctx.Profile.Name == "" {
				return errors.New("profile context required")
			}
			return nil
		}
		_, err = scen.ParseResourceYAML(data, entry)
		require.ErrorContains(t, err, "profile context required")
	})
}

func TestContextualRegistrationIsolation(t *testing.T) {
	recordSideEffects(t)
	calls := make(map[string]int)
	schema := contextualSchema(t, calls)
	document, err := loadContextual(t, schema, contextualDocument)
	require.NoError(t, err)
	original, profile := document.Snapshot(), document.Profile()
	require.NoError(t, schema.ValidateDefinition(original, profile, 2))
	require.Equal(t, original, document.Snapshot())
	require.Equal(t, profile, document.Profile())
	fixture, err := scen.ResolveResource(original, scen.Ref[contextualResource]("fixture"))
	require.NoError(t, err)
	require.Equal(t, []int{7}, fixture.Nested.Values)
	require.Equal(t, []string{"b.txt", "a.txt"}, fixture.Files)
	t.Run("pointer policy rejects offline and concrete inputs", func(t *testing.T) {
		pointerCalls := 0
		pointerSchema, err := scen.WithTypeValidation(schema, func(_ scen.ValidationContext, policy *contextualPolicy) error {
			pointerCalls++
			if policy.Level != 1 {
				return errors.New("pointer policy denied")
			}
			policy.Level = 99
			return nil
		})
		require.NoError(t, err)
		wire := strings.Replace(contextualDocument, "count: 1", "policy: {level: 1}, count: 1", 1)
		loaded, err := loadContextual(t, pointerSchema, wire)
		require.NoError(t, err)
		require.Equal(t, 1, pointerCalls)
		_, err = loadContextual(t, pointerSchema, strings.Replace(wire, "level: 1", "level: 2", 1))
		require.ErrorContains(t, err, "pointer policy denied")
		require.Equal(t, 2, pointerCalls)
		native := loaded.Snapshot()
		input, ok := native.Setup[0].Input.(contextualInput)
		require.True(t, ok)
		input.Policy.Level = 2
		native.Setup[0].Input = input
		require.ErrorContains(t, loaded.ValidateBinding(native, loaded.Profile(), pointerSchema), "pointer policy denied")
		require.Equal(t, 3, pointerCalls)
		require.EqualValues(t, 2, input.Policy.Level)
	})
	for _, tc := range []struct{ name, before, after, diagnostic string }{
		{"format", "code: accepted", "code: rejected", "unrecognized extension code"},
		{"nested", "values: [7]", "values: [8]", "nested profile relationship"},
		{"resource family", "{env: addresses.test}", "2001:db8::7", "resource profile relationship"},
		{"observer context", "value: 7", "value: 8", "observer context relationship"},
		{"reference", "resource: fixture", "resource: absent", "expected resource of type"},
		{"strict boolean presence", ", enabled: false", "", "required field"},
		{"unknown field", "count: 1", "count: 1, extra: 2", "unknown field"},
		{"asset containment", "b.txt", "../outside", "inside fixture root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadContextual(t, schema, strings.Replace(contextualDocument, tc.before, tc.after, 1))
			require.ErrorContains(t, err, tc.diagnostic)
		})
	}
	t.Run("concrete field constraints", func(t *testing.T) {
		native := document.Snapshot()
		input, ok := native.Setup[0].Input.(contextualInput)
		require.True(t, ok)
		input.Count = 10
		native.Setup[0].Input = input
		require.ErrorContains(t, document.ValidateBinding(native, profile, schema), "max 9")
		native = document.Snapshot()
		native.Steps[0].Wait = nil
		require.ErrorContains(t, schema.ValidateDefinition(native, profile, 2), "require wait")
	})
	t.Run("activation permission", func(t *testing.T) {
		profile := document.Profile()
		profile.Services[0].Activation = "other"
		require.ErrorContains(t, document.ValidateBinding(document.Snapshot(), profile, schema), "does not permit activation")
	})
	t.Run("duplicate service ownership", func(t *testing.T) {
		native := document.Snapshot()
		native.Resources["other"] = native.Resources["fixture"]
		_, err := schema.Requirements(native)
		require.ErrorContains(t, err, "already reserved")
	})
	t.Run("registration immutability", func(t *testing.T) {
		_, exists := (scen.Schema{}).LookupResource("extension.fixture")
		require.False(t, exists)
		_, err := schema.WithFormat("extension.code", func(string) error { return nil })
		require.ErrorContains(t, err, "already registered")
		_, err = scen.WithTypeValidation(schema, func(scen.ValidationContext, contextualNested) error { return nil })
		require.ErrorContains(t, err, "already registered")
	})
}

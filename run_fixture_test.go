package boro_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	runresult "github.com/pelageech/boro/result"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	boro "boro"

	scen "github.com/pelageech/boro/scenario"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type testMutation struct {
	Key        string `yaml:"key" rule:"required"`
	Value      int    `yaml:"value" rule:"required"`
	ExpectExit int    `yaml:"expect_exit"`
}

type testRead struct {
	Key string `yaml:"key" rule:"required"`
}

type testWant struct {
	Value int `yaml:"value" rule:"required"`
}

type testMachine struct {
	mu           sync.Mutex
	values       map[string]int
	exitCode     int
	transport    error
	preparations int
	mutate       func(context.Context, *boro.RunScope, testMutation) error
	observe      func(context.Context, testRead) error
	prepare      func(context.Context, *boro.RunScope) error
}

func newTestMachine() *testMachine { return &testMachine{values: make(map[string]int)} }

func (m *testMachine) value(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.values[key]
}

func (m *testMachine) set(key string, value int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
}

type testCapability struct {
	descriptors []scen.Descriptor
	register    func(boro.Bindings) (boro.Bindings, error)
}

func (c *testCapability) Declare(schema scen.Schema) (scen.Schema, error) {
	for _, descriptor := range c.descriptors {
		var err error
		schema, err = schema.WithDescriptor(descriptor)
		if err != nil {
			return scen.Schema{}, err
		}
	}
	return schema, nil
}

func (c *testCapability) Register(bindings boro.Bindings) (boro.Bindings, error) {
	if c.register == nil {
		return bindings, nil
	}
	return c.register(bindings)
}

func composition(t *testing.T, capabilities ...boro.Capability) boro.Composition {
	t.Helper()
	capabilities = append([]boro.Capability{testDeclarations{}}, capabilities...)
	composed, err := boro.Compose("test.framework.v1", capabilities...)
	require.NoError(t, err)
	return composed
}

func (m *testMachine) composition(t *testing.T, extraCapabilities ...boro.Capability) boro.Composition {
	t.Helper()
	return composition(t, append([]boro.Capability{m}, extraCapabilities...)...)
}

func (*testMachine) Declare(schema scen.Schema) (scen.Schema, error) {
	action := scen.Action[testMutation](scen.OperationSpec{Capability: "test"}, func(_ scen.ValidationContext, input testMutation) error {
		if input.ExpectExit < 0 || input.ExpectExit > 255 {
			return fmt.Errorf("invalid expected status: %w", scen.ErrInput)
		}
		return nil
	})
	action.Name = "test.set"
	observer := scen.Observer[testRead, testWant](scen.OperationSpec{Capability: "test"}, nil)
	observer.Name = "test.read"
	return (&testCapability{descriptors: []scen.Descriptor{action, observer}}).Declare(schema)
}

func (m *testMachine) Register(registry boro.Bindings) (boro.Bindings, error) {
	registry, err := boro.BindAction(registry, boro.ActionBinding[testMutation]{
		Spec: scen.OperationSpec{Name: "test.set", Capability: "test"},
		NewPrepare: func() boro.ActionPreparer[testMutation] {
			return func(ctx context.Context, scope *boro.RunScope, input testMutation) (boro.Action, error) {
				m.preparations++
				if m.prepare != nil {
					if err := m.prepare(ctx, scope); err != nil {
						return nil, err
					}
				}
				return func(ctx context.Context) (runresult.Action, error) {
					if m.transport != nil {
						return runresult.Action{}, m.transport
					}
					if input.ExpectExit != m.exitCode {
						return runresult.Action{}, fmt.Errorf("got exit %d, expected %d: %w", m.exitCode, input.ExpectExit, runresult.ErrMismatch)
					}
					if m.exitCode != 0 {
						return runresult.Action{Detail: fmt.Sprintf("expected rejection %d", m.exitCode)}, nil
					}
					if m.mutate != nil {
						if err := m.mutate(ctx, scope, input); err != nil {
							return runresult.Action{}, err
						}
					}
					m.set(input.Key, input.Value)
					return runresult.Action{}, nil
				}, nil
			}
		},
	})
	if err != nil {
		return boro.Bindings{}, err
	}
	registry, err = boro.BindObserver(registry, boro.ObserverBinding[testRead, testWant]{
		Spec: scen.OperationSpec{Name: "test.read", Capability: "test"},
		NewPrepare: func() boro.ObserverPreparer[testRead, testWant] {
			return func(_ context.Context, _ *boro.RunScope, input testRead) (boro.ObservationFunc[testWant], error) {
				m.preparations++
				return func(ctx context.Context, want testWant) (runresult.Observation, error) {
					if m.observe != nil {
						if err := m.observe(ctx, input); err != nil {
							return runresult.Observation{}, err
						}
					}
					value := m.value(input.Key)
					return runresult.Observation{Matches: value == want.Value, Detail: fmt.Sprintf("value=%d", value)}, nil
				}, nil
			}
		},
	})
	return registry, err
}

type runFixture struct {
	body        string
	composition boro.Composition
	invocation  string
	attached    bool
}

func resolveRun(t *testing.T, fixture runFixture) boro.ResolvedScenario {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	yaml := "version: 1\nid: test.engine\nenvironment: {profile: test, allow_attach: true}\n" +
		"timeouts: {scenario: 1m, action: 10s, observation: 1s, poll: 1s}\nresources: {}\n" + fixture.body
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	scenario, err := scen.LoadScenario(t.Context(), path, scen.LoadOptions{Profiles: testProfiles(), Schema: fixture.composition.Schema()})
	require.NoError(t, err)
	if fixture.invocation == "" {
		fixture.invocation = "test-invocation"
	}
	profile := testProfiles()[0]
	environment := boro.EnvironmentManifest{
		Version: 1, InvocationID: fixture.invocation, ScenarioID: scenario.ID(), ScenarioHash: scenario.Hash(),
		Profile: "test", Project: "test-project", RunnerImage: "sha256:test-runner", Capabilities: profile.Capabilities,
	}
	if fixture.attached {
		environment.Ownership = "attached"
	}
	for _, service := range profile.Services {
		environment.Services = append(environment.Services, boro.BoundService{
			Name: service.Name, Roles: service.Roles, Capabilities: service.Capabilities, Families: service.Families,
		})
	}
	resolved, err := boro.Resolve(t.Context(), scenario, environment, fixture.composition)
	require.NoError(t, err)
	return resolved
}

const singleRead = `steps:
  - id: check
    expect: test.read
    with: {key: state}
    want: {value: 0}
    wait: {mode: once}
`

type testDeclarations struct{}

func (testDeclarations) Declare(schema scen.Schema) (scen.Schema, error) {
	return schema.WithDescriptor(scen.Observer[testProbe, testStatus](scen.OperationSpec{Name: "test.status", Capability: "test"}, nil))
}

func (testDeclarations) Register(bindings boro.Bindings) (boro.Bindings, error) {
	return bindings, nil
}

type (
	testProbe struct {
		On scen.Selector `yaml:"on"`
	}
	testStatus struct {
		Accept []string `yaml:"accept"`
	}
)

func testProfiles() []scen.ProfileDeclaration {
	return []scen.ProfileDeclaration{{Name: "test", Capabilities: []string{"test"}, Services: []scen.ServiceDeclaration{{Name: "test-status", Roles: []string{"test-status"}, Capabilities: []string{"test"}}}}}
}

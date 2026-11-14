package suite_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runresult "boro/result"

	"github.com/onsi/ginkgo/v2/types"
	"github.com/stretchr/testify/require"

	"boro"
	scen "boro/scenario"
	"boro/suite"
)

type counterInput struct {
	Value int `yaml:"value"`
}

type counterRead struct{}

type counterState struct {
	Value         int
	Mutations     int
	Samples       int
	Cleanups      int
	OwnerCleanups int
	FaultRestored bool
	HelperJoined  bool
}

var errCounter = errors.New("counter unavailable")

type counterCapability struct {
	state *counterState
	kind  string
}

func (*counterCapability) Declare(schema scen.Schema) (scen.Schema, error) {
	action := scen.Action[counterInput](scen.OperationSpec{Capability: "test"}, nil)
	action.Name = "counter.set"
	observer := scen.Observer[counterRead, counterInput](scen.OperationSpec{Capability: "test"}, nil)
	observer.Name = "counter.read"
	declared, err := schema.WithDescriptor(action)
	if err != nil {
		return scen.Schema{}, err
	}
	return declared.WithDescriptor(observer)
}

func (c *counterCapability) Register(registry boro.Bindings) (boro.Bindings, error) {
	state, kind := c.state, c.kind
	registry, err := boro.BindAction(registry, boro.ActionBinding[counterInput]{
		Spec: scen.OperationSpec{Name: "counter.set", Capability: "test"},
		NewPrepare: func() boro.ActionPreparer[counterInput] {
			return func(_ context.Context, scope *boro.RunScope, input counterInput) (boro.Action, error) {
				if input.Value == 7 {
					if err := scope.Own(boro.TemporaryResource{
						Identity: boro.ResourceIdentity{ID: "counter", Kind: boro.ResourceClient},
						Release: func(ctx context.Context) error {
							state.Cleanups++
							if kind == "cleanup-timeout" {
								<-ctx.Done()
								return nil
							}
							if kind == "cleanup-failure" {
								return errCounter
							}
							return nil
						},
					}); err != nil {
						return nil, err
					}
				}
				return func(ctx context.Context) (runresult.Action, error) {
					state.Mutations++
					switch kind {
					case "owned-fault", "attached-fault":
						err := scope.Own(boro.TemporaryResource{
							Identity: boro.ResourceIdentity{
								ID: "route-fault", Kind: boro.ResourceFault,
								Inspect: "inspect exact route-fault", Cleanup: "restore exact route-fault",
							},
							Release: func(context.Context) error { state.FaultRestored = true; return nil },
						})
						return runresult.Action{}, errors.Join(err, errCounter)
					case "helper-joined":
						helperCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
						done := make(chan struct{})
						err := scope.Own(boro.TemporaryResource{
							Identity: boro.ResourceIdentity{ID: "traffic", Kind: boro.ResourceHelper},
							Stop:     stop,
							Release:  func(context.Context) error { <-done; state.HelperJoined = true; return nil },
						})
						if err != nil {
							stop()
							return runresult.Action{}, err
						}
						go func() { <-helperCtx.Done(); close(done) }()
						return runresult.Action{}, errCounter
					case "scenario-deadline":
						<-ctx.Done()
						return runresult.Action{}, nil
					}
					if kind == "setup-failure" {
						return runresult.Action{}, errCounter
					}
					state.Value = input.Value
					return runresult.Action{}, nil
				}, nil
			}
		},
	})
	if err != nil {
		return boro.Bindings{}, err
	}
	registry, err = boro.BindObserver(registry, boro.ObserverBinding[counterRead, counterInput]{
		Spec: scen.OperationSpec{Name: "counter.read", Capability: "test"},
		NewPrepare: func() boro.ObserverPreparer[counterRead, counterInput] {
			return func(_ context.Context, _ *boro.RunScope, _ counterRead) (boro.ObservationFunc[counterInput], error) {
				if kind == "preparation-failure" {
					return nil, errCounter
				}
				return func(_ context.Context, want counterInput) (runresult.Observation, error) {
					state.Samples++
					if kind == "observer-error" {
						return runresult.Observation{}, errCounter
					}
					return runresult.Observation{Matches: state.Value == want.Value}, nil
				}, nil
			}
		},
	})
	return registry, err
}

func runChild(t *testing.T, kind string) {
	t.Helper()
	var state counterState
	registry, err := boro.Compose("test.suite.counter.v1", &counterCapability{state: &state, kind: kind})
	require.NoError(t, err)
	root := os.Getenv("LABTEST_SUITE_OUTPUT")
	path := filepath.Join(root, "case.yaml")
	source := counterYAML
	if kind == "scenario-deadline" {
		source = strings.Replace(source, "scenario: 5s", "scenario: 50ms", 1)
	}
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	document, err := scen.LoadScenario(t.Context(), path, scen.LoadOptions{Profiles: testProfiles(), Schema: registry.Schema()})
	require.NoError(t, err)
	profile := testProfiles()[0]
	environment := boro.EnvironmentManifest{
		Version: 1, InvocationID: "counter-invocation", ScenarioID: document.ID(), ScenarioHash: document.Hash(),
		Profile: profile.Name, Project: "counter", RunnerImage: "sha256:counter", Capabilities: profile.Capabilities,
	}
	if kind == "attached-fault" {
		environment.Ownership = "attached"
	}
	for _, service := range profile.Services {
		environment.Services = append(environment.Services, boro.BoundService{
			Name: service.Name, Roles: service.Roles, Capabilities: service.Capabilities, Families: service.Families,
		})
	}
	resolved, err := boro.Resolve(t.Context(), document, environment, registry)
	require.NoError(t, err)
	mode := boro.ModeFull
	if kind == "configure" {
		mode = boro.ModeConfigure
	}
	cleanupTimeout := time.Second
	if kind == "cleanup-timeout" {
		cleanupTimeout = 20 * time.Millisecond
	}
	report, runErr := suite.Run(t.Context(), resolved, suite.Options{
		Mode: mode, CleanupTimeout: cleanupTimeout,
		Cleanup: func(context.Context) error {
			state.OwnerCleanups++
			if kind == "owner-cleanup-failure" {
				return errCounter
			}
			return nil
		},
	})
	validationErr := boro.ValidateSuiteReport(resolved, mode, report)
	if runErr == nil {
		require.NoError(t, validationErr)
	} else {
		require.Error(t, validationErr)
	}
	reports, err := json.Marshal([]types.Report{report})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "report.json"), reports, 0o600))
	counter, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "counter.json"), counter, 0o600))
	if runErr != nil {
		t.Fatal(runErr)
	}
}

const counterYAML = `version: 1
id: counter.lifecycle
environment: {profile: test, allow_attach: true}
timeouts: {scenario: 5s, action: 1s, observation: 1s, poll: 1ms}
resources: {}
setup:
  - id: set
    do: counter.set
    with: {value: 7}
steps:
  - id: read
    expect: counter.read
    with: {}
    want: {value: 7}
    wait: {mode: eventually, timeout: 1s}
teardown:
  - id: clear
    do: counter.set
    with: {value: 0}
`

func testProfiles() []scen.ProfileDeclaration {
	return []scen.ProfileDeclaration{{Name: "test", Capabilities: []string{"test"}, Services: []scen.ServiceDeclaration{{Name: "test-status", Roles: []string{"test-status"}, Capabilities: []string{"test"}}}}}
}

package counter_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"boro"
	command "boro/command"
	"boro/examples/counter"
	"boro/examples/counter/schema"
	"boro/scenario"
)

type backend struct {
	mu                      sync.Mutex
	values                  map[string]int
	created, closed, active int
	partial, blocked        bool
}

type client struct {
	backend *backend
	key     string
}

var errClient = errors.New("counter acquisition failed")

func (b *backend) factory(_ context.Context, env boro.EnvironmentManifest) (counter.Backend, error) {
	return &envBackend{backend: b, env: env}, nil
}

type envBackend struct {
	backend *backend
	env     boro.EnvironmentManifest
}

func (e *envBackend) Open(_ context.Context, value counter.Spec) (counter.Client, error) {
	b := e.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	b.created++
	c := &client{backend: b, key: e.env.InvocationID + "/" + value.Name}
	if b.partial {
		return c, errClient
	}
	return c, nil
}

func (c *client) Add(_ context.Context, by int) error {
	c.backend.mu.Lock()
	defer c.backend.mu.Unlock()
	c.backend.values[c.key] += by
	return nil
}

func (c *client) Value(ctx context.Context) (int, error) {
	b := c.backend
	b.mu.Lock()
	if !b.blocked {
		value := b.values[c.key]
		b.mu.Unlock()
		return value, nil
	}
	b.active++
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.active--; b.mu.Unlock() }()
	<-ctx.Done()
	return 0, ctx.Err()
}

func (c *client) Close(ctx context.Context) error {
	if _, bounded := ctx.Deadline(); !bounded || ctx.Err() != nil {
		return fmt.Errorf("cleanup requires a fresh bounded context: %w", context.DeadlineExceeded)
	}
	c.backend.mu.Lock()
	defer c.backend.mu.Unlock()
	if c.backend.active != 0 {
		return errors.New("client closed before observations joined")
	}
	c.backend.closed++
	return nil
}

func composition(t *testing.T, b *backend) (scenario.Schema, boro.Composition) {
	t.Helper()
	capability, err := counter.NewCapability(b.factory)
	require.NoError(t, err)
	composition, err := boro.Compose("example.counter.v1", capability)
	require.NoError(t, err)
	require.Zero(t, b.created, "composition must not acquire clients")
	return composition.Schema(), composition
}

func TestRejectsBeforeAcquisition(t *testing.T) {
	source, err := os.ReadFile("testdata/scenario.yaml")
	require.NoError(t, err)
	b := &backend{values: make(map[string]int)}
	pure, runtime := composition(t, b)
	// The counter capability is self-contained: loading its scenario against a
	// profile set that lacks "counter" must reject, with no production catalog.
	foreign := []scenario.ProfileDeclaration{{Name: "foreign", Capabilities: []string{"foreign"}}}
	_, err = scenario.LoadScenario(t.Context(), "testdata/scenario.yaml", scenario.LoadOptions{Profiles: foreign, Schema: pure})
	require.ErrorContains(t, err, "unknown profile counter")
	require.Zero(t, b.created)
	for _, test := range []struct {
		name, old, replacement, diagnostic string
	}{
		{"validator", "by: 2", "by: 0", "increment must be positive"},
		{"typed reference", "counter: requests", "counter: missing", "missing"},
		{"expectation validator", "want: {value: 2}", "want: {value: -1}", "nonnegative"},
		{"unknown field", "spec: {name: requests}", "spec: {name: requests, extra: true}", "extra"},
		{"malformed integer", "by: 2", "by: wrong", "by"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "case.yaml")
			require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(source), test.old, test.replacement, 1)), 0o600))
			for _, selected := range []scenario.Schema{pure, runtime.Schema()} {
				_, err := scenario.LoadScenario(t.Context(), path, scenario.LoadOptions{
					Schema: selected, Profiles: []scenario.ProfileDeclaration{schema.Profile()},
				})
				require.ErrorContains(t, err, test.diagnostic)
			}
			require.Zero(t, b.created)
		})
	}
}

func TestNativeCounter(t *testing.T) {
	if kind := os.Getenv("COUNTER_CHILD"); kind != "" {
		runChild(t, kind)
		return
	}
	for _, test := range []struct {
		name           string
		pass           bool
		value, clients int
	}{
		{"full", true, 3, 2},
		{"configure", true, 2, 1},
		{"partial-acquisition", false, 0, 1},
		{"canceled-observation", false, 2, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			executable, err := os.Executable()
			require.NoError(t, err)
			command := exec.CommandContext(t.Context(), executable, "-test.run=^TestNativeCounter$", "-test.v")
			command.Env = append(os.Environ(), "COUNTER_CHILD="+test.name, "COUNTER_OUTPUT="+root)
			output, runErr := command.CombinedOutput()
			if test.pass {
				require.NoError(t, runErr, "%s", output)
			} else {
				var exit *exec.ExitError
				require.ErrorAs(t, runErr, &exit, "%s", output)
			}
			file, err := os.Open(filepath.Join(root, "report.json"))
			require.NoError(t, err, "%s", output)
			report, err := boro.ReadSuiteReport(file)
			require.NoError(t, err)
			require.NoError(t, file.Close())
			require.Equal(t, test.pass, report.SuiteSucceeded)
			data, err := os.ReadFile(filepath.Join(root, "clients.json"))
			require.NoError(t, err)
			var observed struct{ Value, Created, Closed, Active int }
			require.NoError(t, json.Unmarshal(data, &observed))
			require.Equal(t, test.value, observed.Value)
			require.Equal(t, test.clients, observed.Created)
			require.Equal(t, observed.Created, observed.Closed)
			require.Zero(t, observed.Active)
		})
	}
}

func TestCounterReleasesClientWhenOwnershipFails(t *testing.T) {
	for _, test := range []struct {
		name    string
		partial bool
	}{{"successful acquisition", false}, {"partial acquisition", true}} {
		t.Run(test.name, func(t *testing.T) {
			b := &backend{partial: test.partial}
			_, registry := composition(t, b)
			scope := boro.NewRunScope(scenario.Definition{
				Resources: map[scenario.Name]scenario.Resource{
					"requests": {Type: "counter.fixture", Spec: schema.Counter{Name: "requests"}},
				},
			}, boro.EnvironmentManifest{}, nil)
			_, err := scope.Close(t.Context(), false)
			require.NoError(t, err)
			_, err = registry.Prepare(t.Context(), scope, scenario.Step{
				Operation: "counter.increment",
				Input:     schema.IncrementInput{Counter: "requests", By: 1},
			})
			if test.partial {
				require.ErrorIs(t, err, errClient)
				require.NotContains(t, err.Error(), "already joined and closed", "partial constructor failure is reported at acquisition, not ownership")
			} else {
				require.ErrorContains(t, err, "already joined and closed")
			}
			require.Equal(t, 1, b.created)
			require.Equal(t, 1, b.closed, "failed ownership transfer must not leak")
		})
	}
}

func TestCounterManagedUsesObject(t *testing.T) {
	b := &backend{values: make(map[string]int)}
	_, registry := composition(t, b)
	scope := boro.NewRunScope(scenario.Definition{
		Resources: map[scenario.Name]scenario.Resource{
			"requests": {Type: "counter.fixture", Spec: schema.Counter{Name: "requests"}},
		},
	}, boro.EnvironmentManifest{}, nil)
	prepared, err := registry.Prepare(t.Context(), scope, scenario.Step{
		Operation: "counter.increment",
		Input:     schema.IncrementInput{Counter: "requests", By: 0},
	})
	require.NoError(t, err)
	require.NotNil(t, prepared.Action, "method validation belongs to the managed call, not preparation")
	_, err = prepared.Action(t.Context())
	require.ErrorContains(t, err, "validate increment")
	require.ErrorIs(t, err, scenario.ErrInput)
	b.mu.Lock()
	require.Empty(t, b.values, "invalid increment must never reach the backend")
	b.mu.Unlock()
	require.Equal(t, 1, b.created, "preparation acquires the managed counter once")
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	_, err = scope.Close(cleanup, false)
	require.NoError(t, err)
	require.Equal(t, 1, b.closed)
}

func runChild(t *testing.T, kind string) {
	t.Helper()
	b := &backend{
		values: make(map[string]int), partial: kind == "partial-acquisition", blocked: kind == "canceled-observation",
	}
	pure, registry := composition(t, b)
	profile := schema.Profile()
	path, err := filepath.Abs("testdata/scenario.yaml")
	require.NoError(t, err)
	document, err := scenario.LoadScenario(t.Context(), path, scenario.LoadOptions{
		Schema: pure, Profiles: []scenario.ProfileDeclaration{profile},
	})
	require.NoError(t, err)
	env := boro.EnvironmentManifest{
		Version: 1, InvocationID: "counter-" + kind, ScenarioID: document.ID(), ScenarioHash: document.Hash(),
		Profile: profile.Name, Project: "counter", RunnerImage: "counter-fake", Capabilities: profile.Capabilities,
		Services: []boro.BoundService{{
			Name: "counter", Roles: []string{"counter"}, Capabilities: []string{"counter"},
		}},
	}
	root := os.Getenv("COUNTER_OUTPUT")
	environment := filepath.Join(root, "environment.json")
	data, err := json.Marshal(env)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(environment, data, 0o600))
	mode := boro.ModeFull
	if kind == "configure" {
		mode = boro.ModeConfigure
	}
	command := command.New(command.Options{
		Composition: registry, Profiles: []scenario.ProfileDeclaration{profile},
	})
	command.SetArgs([]string{
		"run", "--scenario", path, "--environment", environment, "--invocation", env.InvocationID,
		"--mode", string(mode), "--result", filepath.Join(root, "report.json"),
	})
	runErr := command.ExecuteContext(t.Context())
	b.mu.Lock()
	state := struct{ Value, Created, Closed, Active int }{
		b.values[env.InvocationID+"/requests"], b.created, b.closed, b.active,
	}
	b.mu.Unlock()
	data, err = json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "clients.json"), data, 0o600))
	if runErr != nil {
		t.Fatal(runErr)
	}
}

package boro_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2/types"
	"github.com/stretchr/testify/require"

	"github.com/pelageech/boro"
	"github.com/pelageech/boro/result"
	"github.com/pelageech/boro/scenario"
	"github.com/pelageech/boro/suite"
)

const (
	externalRevision   = "test.external.v1"
	externalDomain     = "ext.report"
	externalService    = "ext-svc"
	externalActivation = "ext.capture"
	externalContainer  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type externalConfig struct {
	MaxEntries int      `yaml:"max_entries" rule:"required" min:"1" max:"100"`
	Labels     []string `yaml:"labels" rule:"required,nonempty"`
}

type externalReport struct {
	Service string         `yaml:"service" rule:"required"`
	Path    string         `yaml:"path" rule:"required"`
	Token   string         `yaml:"token" rule:"required" format:"ext-token"`
	Config  externalConfig `yaml:"config" rule:"required"`
}

type externalInput struct {
	Report scenario.Ref[externalReport] `yaml:"report" rule:"required"`
	On     scenario.Selector            `yaml:"on" rule:"required"`
}

type externalWant struct {
	Entries []string `yaml:"entries" rule:"required,nonempty"`
}
type externalResetScope struct {
	Service string `json:"service"`
}
type externalResetExpected struct {
	Entries []string `json:"entries"`
}
type externalResetActual struct {
	Entries []string `json:"entries"`
}

// All declarations and runtime bindings in this capability use public APIs.
// HTTP state is shared by the parent, native suite child, and real reset.
type externalCapability struct {
	acquisitions atomic.Int32
	releases     atomic.Int32
	validations  atomic.Int32
}

func externalProfile() scenario.ProfileDeclaration {
	return scenario.ProfileDeclaration{Name: "ext", Capabilities: []string{"ext"}, Services: []scenario.ServiceDeclaration{{
		Name: externalService, Capabilities: []string{"ext"}, Activation: externalActivation,
	}}}
}

func externalInputCheck(ctx scenario.ValidationContext, input externalInput) error {
	spec, err := scenario.ResolveResource(ctx.Definition, input.Report)
	if err != nil {
		return err
	}
	if input.On.Service != spec.Service {
		return fmt.Errorf("report and selector services differ: %w", scenario.ErrInput)
	}
	return nil
}

func (c *externalCapability) Declare(s scenario.Schema) (scenario.Schema, error) {
	s, err := s.WithFormat("ext-token", func(token string) error {
		if !strings.HasPrefix(token, "ext-") {
			return fmt.Errorf("invalid extension token: %w", scenario.ErrInput)
		}
		return nil
	})
	if err != nil {
		return scenario.Schema{}, err
	}
	s, err = scenario.WithTypeValidation(s, func(_ scenario.ValidationContext, config externalConfig) error {
		if config.MaxEntries < len(config.Labels) {
			return fmt.Errorf("labels exceed max_entries: %w", scenario.ErrInput)
		}
		return nil
	})
	if err != nil {
		return scenario.Schema{}, err
	}
	s, err = scenario.WithResource(s, scenario.ResourceRegistration[externalReport]{
		Spec: scenario.ResourceSpec{Name: externalDomain, Capability: "ext"},
		Validate: func(ctx scenario.ValidationContext, spec externalReport) error {
			c.validations.Add(1)
			_, err := ctx.Profile.SelectServices(scenario.Selector{Service: spec.Service}, "ext")
			return err
		},
		Assets: func(spec externalReport) ([]scenario.AssetSource, error) {
			return []scenario.AssetSource{{Path: spec.Path, Field: "path"}}, nil
		},
		Requirements: func(spec externalReport) (scenario.ResourceRequirements, error) {
			return scenario.ResourceRequirements{Activations: []scenario.ServiceActivation{{Service: spec.Service, Activation: externalActivation}}}, nil
		},
	})
	if err != nil {
		return scenario.Schema{}, err
	}
	s, err = s.WithDescriptor(scenario.Action[externalInput](scenario.OperationSpec{Name: "ext.apply", Capability: "ext", ResetDomain: externalDomain}, externalInputCheck))
	if err != nil {
		return scenario.Schema{}, err
	}
	s, err = s.WithDescriptor(scenario.Observer[externalInput, externalWant](
		scenario.OperationSpec{Name: "ext.sample", Capability: "ext"},
		func(ctx scenario.ValidationContext, input externalInput, _ externalWant) error {
			return externalInputCheck(ctx, input)
		},
	))
	if err != nil {
		return scenario.Schema{}, err
	}
	s, err = s.WithResetDomain(scenario.ResetDomainRegistration{Name: externalDomain, Validate: func(_ scenario.ResetBaseline, domain scenario.ResetDomain) error {
		if len(domain.AssetIDs) != 0 || domain.Mode != "" {
			return fmt.Errorf("unexpected extension reset assets or mode: %w", scenario.ErrInput)
		}
		return nil
	}})
	if err != nil {
		return scenario.Schema{}, err
	}
	s, err = scenario.WithResetCheck(s, scenario.ResetCheckRegistration[externalResetScope, externalResetExpected, externalResetActual]{
		Kind: "ext.entries", Domains: []string{externalDomain},
		Validate: func(ctx scenario.ResetValidationContext, scope externalResetScope, want externalResetExpected) error {
			if scope.Service != ctx.Target.Service || len(want.Entries) == 0 {
				return fmt.Errorf("invalid extension reset scope: %w", scenario.ErrInput)
			}
			return nil
		},
		ValidateActual: func(actual externalResetActual) error {
			if actual.Entries == nil {
				return fmt.Errorf("absent extension inventory: %w", scenario.ErrInput)
			}
			if slices.Contains(actual.Entries, "invalid") {
				return fmt.Errorf("invalid extension inventory entry: %w", scenario.ErrInput)
			}
			return nil
		},
		Compare: func(want externalResetExpected, got externalResetActual) (bool, error) {
			return slices.Equal(slices.Sorted(slices.Values(want.Entries)), slices.Sorted(slices.Values(got.Entries))), nil
		},
	})
	if err != nil {
		return scenario.Schema{}, err
	}
	return s.WithResetProfile(scenario.ResetProfilePolicy{Profile: "ext", RequiredDomains: []string{externalDomain}, AllowedDomains: []string{externalDomain}})
}

// The acquired private HTTP transport has one release authority, RunScope.
func (c *externalCapability) acquire(ctx context.Context, scope *boro.RunScope, id string) (*http.Client, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	service, err := boro.BoundServiceByName(scope.Environment(), externalService)
	if err != nil {
		return nil, "", err
	}
	endpoint := service.Endpoints["ext"]
	transport := &http.Transport{
		DialContext:  (&net.Dialer{Timeout: time.Second}).DialContext,
		MaxIdleConns: 1, MaxIdleConnsPerHost: 1, IdleConnTimeout: time.Second,
		ResponseHeaderTimeout: time.Second, TLSHandshakeTimeout: time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	c.acquisitions.Add(1)
	release := func(ctx context.Context) error { transport.CloseIdleConnections(); c.releases.Add(1); return ctx.Err() }
	if err := scope.Own(boro.TemporaryResource{
		Identity: boro.ResourceIdentity{ID: id, Kind: boro.ResourceClient}, Target: externalService,
		Locator: boro.ResetLocator{Type: "client", Protocol: "http", Endpoint: endpoint}, Release: release,
	}); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		return nil, "", errors.Join(err, release(cleanup))
	}
	return client, endpoint, nil
}

func externalCall(ctx context.Context, client *http.Client, endpoint, method string, entries []string) (actual externalResetActual, err error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(strings.Join(entries, ",")))
	if err != nil {
		return actual, err
	}
	response, err := client.Do(request)
	if err != nil {
		return actual, err
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return actual, err
	}
	if response.StatusCode != http.StatusOK || len(data) > 4096 {
		return actual, fmt.Errorf("extension HTTP status/size %d/%d: %w", response.StatusCode, len(data), scenario.ErrInput)
	}
	actual.Entries = []string{}
	if len(data) != 0 {
		actual.Entries = strings.Split(string(data), ",")
	}
	return actual, nil
}

func (c *externalCapability) Register(b boro.Bindings) (boro.Bindings, error) {
	b, err := boro.BindAction(b, boro.ActionBinding[externalInput]{
		Spec: scenario.OperationSpec{Name: "ext.apply", Capability: "ext", ResetDomain: externalDomain},
		NewPrepare: func() boro.ActionPreparer[externalInput] {
			return func(ctx context.Context, scope *boro.RunScope, input externalInput) (boro.Action, error) {
				spec, err := scenario.ResolveResource(scope.Scenario(), input.Report)
				if err != nil {
					return nil, err
				}
				var contents []byte
				for _, asset := range scope.Assets() {
					if asset.Resource == scenario.Name(input.Report) && asset.Path == spec.Path {
						contents = asset.Contents
					}
				}
				if contents == nil {
					return nil, fmt.Errorf("missing captured extension asset: %w", scenario.ErrInput)
				}
				client, endpoint, err := c.acquire(ctx, scope, "ext-apply")
				if err != nil {
					return nil, err
				}
				entries := strings.Split(string(contents), ",")
				return func(ctx context.Context) (result.Action, error) {
					_, err := externalCall(ctx, client, endpoint, http.MethodPut, entries)
					return result.Action{Detail: string(contents)}, err
				}, nil
			}
		},
	})
	if err != nil {
		return boro.Bindings{}, err
	}
	b, err = boro.BindObserver(b, boro.ObserverBinding[externalInput, externalWant]{
		Spec: scenario.OperationSpec{Name: "ext.sample", Capability: "ext"},
		NewPrepare: func() boro.ObserverPreparer[externalInput, externalWant] {
			return func(ctx context.Context, scope *boro.RunScope, _ externalInput) (boro.ObservationFunc[externalWant], error) {
				client, endpoint, err := c.acquire(ctx, scope, "ext-observe")
				if err != nil {
					return nil, err
				}
				return func(ctx context.Context, want externalWant) (result.Observation, error) {
					actual, err := externalCall(ctx, client, endpoint, http.MethodGet, nil)
					return result.Observation{Matches: slices.Equal(slices.Sorted(slices.Values(actual.Entries)), want.Entries), Detail: strings.Join(actual.Entries, ",")}, err
				}, nil
			}
		},
	})
	if err != nil {
		return boro.Bindings{}, err
	}
	return boro.BindReset(b, externalDomain, boro.ResetHandler{
		Validate: func(resolved boro.ResolvedScenario, baseline scenario.ResetBaseline) error {
			for _, check := range baseline.ExpectedChecks {
				if _, err := boro.BoundServiceByName(resolved.Environment(), check.Target.Service); err != nil {
					return err
				}
			}
			return nil
		},
		Prepare: func(
			ctx context.Context, scope *boro.RunScope, _ scenario.ResetBaseline, _ scenario.ExpectedCheck,
		) (func(context.Context) (scenario.ResetValue, error), error) {
			client, endpoint, err := c.acquire(ctx, scope, "ext-reset-observe")
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (scenario.ResetValue, error) {
				return externalCall(ctx, client, endpoint, http.MethodGet, nil)
			}, nil
		},
		PrepareRestore: func(ctx context.Context, scope *boro.RunScope, baseline scenario.ResetBaseline) ([]boro.Action, error) {
			expected, err := scenario.ResetTyped[externalResetExpected](baseline.ExpectedChecks[0].Expected)
			if err != nil {
				return nil, err
			}
			client, endpoint, err := c.acquire(ctx, scope, "ext-reset-restore")
			if err != nil {
				return nil, err
			}
			return []boro.Action{func(ctx context.Context) (result.Action, error) {
				_, err := externalCall(ctx, client, endpoint, http.MethodPut, expected.Entries)
				return result.Action{Detail: "restore seed"}, err
			}}, nil
		},
	})
}

const externalScenarioYAML = `version: 1
id: ext.e2e
environment: {profile: ext}
timeouts: {scenario: 30s, action: 5s, observation: 2s, poll: 100ms}
resources:
  report:
    type: ext.report
    spec:
      service: ext-svc
      path: report.bin
      token: ext-valid
      config: {max_entries: 2, labels: [stable]}
setup:
  - {id: apply, do: ext.apply, with: {report: report, on: {service: ext-svc}}}
steps:
  - {id: sample, expect: ext.sample, with: {report: report, on: {service: ext-svc}}, want: {entries: [alpha, beta]}, wait: {mode: once}}
`

const externalBaselineJSON = "{\"version\":1,\"id\":\"ext-baseline\",\"profile\":\"ext\",\"registry\":\"boro.v1\",\"domains\":[{\"name\":\"ext.report\",\"asse" +
	"t_ids\":[]}],\"assets\":[],\"expected_checks\":[{\"id\":\"entries\",\"domain\":\"ext.report\",\"target\":{\"service\":\"ext-svc\"" +
	"},\"kind\":\"ext.entries\",\"scope\":{\"service\":\"ext-svc\"},\"expected\":{\"entries\":[\"seed\"]}}]}"

func externalFiles(t *testing.T, body string) (string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "scenario.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "report.bin"), []byte("beta,alpha"), 0o600))
	return path, root
}

func externalComposition(t *testing.T, c *externalCapability) boro.Composition {
	t.Helper()
	composition, err := boro.Compose(externalRevision, c)
	require.NoError(t, err)
	return composition
}

func externalLoad(t *testing.T, path string, composition boro.Composition) scenario.Scenario {
	t.Helper()
	document, err := scenario.LoadScenario(t.Context(), path, scenario.LoadOptions{
		FixtureRoot: filepath.Dir(path),
		Profiles:    []scenario.ProfileDeclaration{externalProfile()},
		Schema:      composition.Schema(),
	})
	require.NoError(t, err)
	return document
}

func externalEnvironment(document scenario.Scenario, endpoint string) boro.EnvironmentManifest {
	return boro.EnvironmentManifest{
		Version: 1, InvocationID: "ext-invocation", ScenarioID: document.ID(), ScenarioHash: document.Hash(),
		Profile: "ext", Project: "ext-project", RunnerImage: "sha256:" + strings.Repeat("d", 64),
		Ownership: "owned", Capabilities: []string{"ext"}, Services: []boro.BoundService{{
			Name: externalService, Capabilities: []string{"ext"}, Activation: externalActivation, Endpoints: map[string]string{"ext": endpoint, "container_id": externalContainer},
		}},
	}
}

type externalWire struct {
	mu                      sync.Mutex
	entries                 []string
	reads, writes, failures int
}

func (w *externalWire) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch request.Method {
	case http.MethodPut:
		data, err := io.ReadAll(io.LimitReader(request.Body, 4097))
		if err != nil || len(data) > 4096 {
			w.failures++
			http.Error(response, "bad body", http.StatusBadRequest)
			return
		}
		w.entries = strings.Split(string(data), ",")
		w.writes++
	case http.MethodGet:
		w.reads++
	default:
		w.failures++
		http.Error(response, "bad method", http.StatusMethodNotAllowed)
		return
	}
	if _, err := io.WriteString(response, strings.Join(w.entries, ",")); err != nil {
		w.failures++
	}
}

type externalChildProof struct {
	Contract           boro.Contract
	Identity           boro.ResultIdentity
	Acquired, Released int32
}

// A re-exec of this same named test runs exactly one native Ginkgo suite.
func externalChild(t *testing.T) {
	t.Helper()
	c := &externalCapability{}
	composition := externalComposition(t, c)
	path := os.Getenv("LABTEST_EXT_SCENARIO")
	document := externalLoad(t, path, composition)
	environment := externalEnvironment(document, os.Getenv("LABTEST_EXT_ENDPOINT"))
	resolved, err := boro.Resolve(t.Context(), document, environment, composition)
	require.NoError(t, err)
	// Runtime must consume the captured bytes, not reopen this changed file.
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "report.bin"), []byte("tampered"), 0o600))
	report, err := suite.Run(t.Context(), resolved, suite.Options{Mode: boro.ModeFull, CleanupTimeout: 5 * time.Second})
	require.NoError(t, err)
	require.NoError(t, boro.ValidateSuiteReport(resolved, boro.ModeFull, report))
	data, err := json.Marshal([]types.Report{report})
	require.NoError(t, err)
	output := os.Getenv("LABTEST_EXT_REPORT")
	require.NoError(t, os.WriteFile(output, data, 0o600))
	proof := externalChildProof{composition.Contract(), resolved.Identity(), c.acquisitions.Load(), c.releases.Load()}
	require.EqualValues(t, 2, proof.Acquired)
	require.Equal(t, proof.Acquired, proof.Released)
	data, err = json.Marshal(proof)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output+".proof.json", data, 0o600))
	// This branch is a foreground runner application. Ginkgo retains its signal
	// watcher for the process lifetime, so it must terminate here, rather than
	// return to the parent package's no-native-suite goleak TestMain. All owned
	// clients have already joined and released, with receipts checked above.
	fmt.Printf("EXTERNAL_CHILD_READY acquired=%d released=%d\n", proof.Acquired, proof.Released)
	os.Exit(0)
}

// TestExternalCapabilityEndToEnd is an external SDK consumer, including real
// shared wire state, a joined child report, resource receipts and typed reset.
func TestExternalCapabilityEndToEnd(t *testing.T) {
	if os.Getenv("LABTEST_EXT_CHILD") == "1" {
		externalChild(t)
		return
	}
	c := &externalCapability{}
	composition := externalComposition(t, c)
	path, _ := externalFiles(t, externalScenarioYAML)
	document := externalLoad(t, path, composition)
	require.Zero(t, c.acquisitions.Load())
	require.EqualValues(t, 1, c.validations.Load())
	assets := document.Assets()
	require.Len(t, assets, 1)
	require.Equal(t, scenario.Digest([]byte("beta,alpha")), assets[0].SHA256)
	require.Equal(t, []byte("beta,alpha"), assets[0].Contents)
	assets[0].Contents[0] = 'X'
	require.Equal(t, []byte("beta,alpha"), document.Assets()[0].Contents)
	requirements, err := composition.Schema().Requirements(document.Snapshot())
	require.NoError(t, err)
	require.Equal(t, []scenario.ServiceActivation{{Service: externalService, Activation: externalActivation}}, requirements.Activations)
	wire := &externalWire{entries: []string{"seed"}}
	server := httptest.NewServer(wire)
	t.Cleanup(server.Close)
	environment := externalEnvironment(document, server.URL)
	resolved, err := boro.Resolve(t.Context(), document, environment, composition)
	require.NoError(t, err)
	require.EqualValues(t, 2, c.validations.Load(), "contextual checks rerun at binding")
	t.Run("direct public invocation", func(t *testing.T) {
		scope, prepared, err := resolved.PrepareInvocation(t.Context(), boro.ModeFull)
		require.NotNil(t, scope)
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
			defer cancel()
			receipts, err := scope.Close(cleanup, false)
			require.NoError(t, err)
			require.Len(t, receipts, 2)
		})
		require.NoError(t, err)
		_, err = prepared["apply"].Action(t.Context())
		require.NoError(t, err)
		sample, err := prepared["sample"].Observe(t.Context())
		require.NoError(t, err)
		require.True(t, sample.Matches, sample.Detail)
	})
	t.Run("negative sample", func(t *testing.T) {
		scope := resolved.NewRunScope()
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
			defer cancel()
			_, err := scope.Close(cleanup, false)
			require.NoError(t, err)
		})
		step := resolved.Snapshot().Steps[0]
		step.Want = externalWant{Entries: []string{"wrong"}}
		prepared, err := composition.Prepare(t.Context(), scope, step)
		require.NoError(t, err)
		sample, err := prepared.Observe(t.Context())
		require.NoError(t, err)
		require.False(t, sample.Matches)
	})
	require.EqualValues(t, 3, c.acquisitions.Load())
	require.Equal(t, c.acquisitions.Load(), c.releases.Load())
	output := filepath.Join(t.TempDir(), "report.json")
	childCtx, stop := context.WithTimeout(t.Context(), 2*time.Minute)
	defer stop()
	executable, err := os.Executable()
	require.NoError(t, err)
	child := exec.CommandContext(childCtx, executable, "-test.run=^TestExternalCapabilityEndToEnd$", "-test.v")
	child.Env = append(os.Environ(), "LABTEST_EXT_CHILD=1", "LABTEST_EXT_SCENARIO="+path, "LABTEST_EXT_ENDPOINT="+server.URL, "LABTEST_EXT_REPORT="+output)
	childOutput, err := child.CombinedOutput()
	require.NoError(t, err, "%s", childOutput)
	reportBytes, err := os.ReadFile(output)
	require.NoError(t, err)
	report, err := boro.ReadSuiteReport(bytes.NewReader(reportBytes))
	require.NoError(t, err)
	require.NoError(t, boro.ValidateSuiteReport(resolved, boro.ModeFull, report))
	proofBytes, err := os.ReadFile(output + ".proof.json")
	require.NoError(t, err)
	var proof externalChildProof
	require.NoError(t, json.Unmarshal(proofBytes, &proof))
	require.NoError(t, composition.Contract().RequireCompatible(proof.Contract))
	require.Equal(t, resolved.Identity(), proof.Identity)
	require.EqualValues(t, 2, proof.Acquired)
	require.Equal(t, proof.Acquired, proof.Released)
	// The child changed the source asset after loading; captured bytes still won.
	wire.mu.Lock()
	actual := slices.Clone(wire.entries)
	wire.mu.Unlock()
	require.Equal(t, []string{"beta", "alpha"}, actual)
	mutated := externalLoad(t, path, composition)
	require.NotEqual(t, document.Hash(), mutated.Hash())
	baseline, err := composition.Schema().ReadResetBaseline(strings.NewReader(externalBaselineJSON))
	require.NoError(t, err)
	require.Equal(t, scenario.Digest([]byte(externalBaselineJSON)), baseline.SHA256())
	owner := boro.ResetContext{
		Identity: resolved.Identity(), Generation: boro.GenerationIdentity{
			BatchID: "ext-batch", Generation: 1, Project: environment.Project, CompatibilityHash: strings.Repeat("e", 64),
		},
		Baseline: boro.BaselineIdentity{ID: baseline.ID, SHA256: baseline.SHA256()},
		ResetID:  "ext-reset", Boundary: 1, CaseFinishedAt: time.Now().UTC(), CaseProcessExit: 0, CaseHelpersJoined: true,
		BoundTargets: []scenario.BoundResetTarget{{Service: externalService, ContainerID: externalContainer}},
	}
	events := &bytes.Buffer{}
	receipt, err := boro.Reset(t.Context(), resolved, boro.ResetOptions{
		Baseline: baseline, Context: owner, Execution: report,
		Timeout: 10 * time.Second, CleanupTimeout: 5 * time.Second, Events: events,
	})
	require.NoError(t, err)
	require.Empty(t, receipt.Errors)
	require.Len(t, receipt.Resources, 2)
	require.Len(t, receipt.Observations, 1)
	require.Equal(t, externalResetActual{Entries: []string{"seed"}}, receipt.Observations[0].Actual)
	require.Equal(t, resolved.Identity(), receipt.Identity)
	for _, resource := range receipt.Resources {
		require.True(t, resource.Joined)
		require.Equal(t, boro.ResourceReleased, resource.State)
	}
	receiptBytes, err := json.Marshal(receipt)
	require.NoError(t, err)
	decoded, err := boro.ReadResetReceipt(bytes.NewReader(receiptBytes), baseline, composition.Schema())
	require.NoError(t, err)
	require.Equal(t, receipt, decoded)
	journal, err := boro.ReadResetEvents(bytes.NewReader(events.Bytes()))
	require.NoError(t, err)
	require.Len(t, journal, 4)
	require.NoError(t, boro.ReconcileReset(resolved, baseline, owner, report, decoded, boro.ResetCompletion{ExitCode: 0, FinishedAt: time.Now().UTC(), Events: journal}))
	stale := owner
	stale.Identity.InvocationID = "another"
	require.Error(t, boro.ReconcileReset(resolved, baseline, stale, report, decoded, boro.ResetCompletion{FinishedAt: time.Now().UTC(), Events: journal}))
	require.EqualValues(t, 5, c.acquisitions.Load())
	require.Equal(t, c.acquisitions.Load(), c.releases.Load())
	wire.mu.Lock()
	reads, writes, failures := wire.reads, wire.writes, wire.failures
	actual = slices.Clone(wire.entries)
	wire.mu.Unlock()
	require.Equal(t, []string{"seed"}, actual)
	require.Equal(t, 4, reads)
	require.Equal(t, 3, writes)
	require.Zero(t, failures)
	t.Logf("child_pid=%d identity=%+v source=%s asset=%s contract=%+v "+
		"parent_acquired=%d released=%d child_acquired=%d released=%d reset_events=%d reads=%d writes=%d errors=%d",
		child.Process.Pid, resolved.Identity(), document.Source(), document.Assets()[0].SHA256, proof.Contract,
		c.acquisitions.Load(), c.releases.Load(), proof.Acquired, proof.Released, len(journal), reads, writes, failures)
	if evidence := os.Getenv("LABTEST_TASK8_EVIDENCE"); evidence != "" {
		require.NoError(t, os.MkdirAll(evidence, 0o755))
		for name, data := range map[string][]byte{
			"external-report.json": reportBytes, "external-receipt.json": receiptBytes, "external-events.jsonl": events.Bytes(),
			"external-child.json": proofBytes, "external-child.log": childOutput,
			"external-baseline.json": []byte(externalBaselineJSON), "external-source.yaml": []byte(externalScenarioYAML),
		} {
			require.NoError(t, os.WriteFile(filepath.Join(evidence, name), data, 0o600))
		}
	}
}

func TestExternalCapabilityRejectsBeforeAcquisition(t *testing.T) {
	for _, test := range []struct {
		name, old, replacement, reason string
		assetMissing                   bool
	}{
		{name: "badprofile", old: "service: ext-svc\n      path", replacement: "service: absent\n      path", reason: "selector resolves to no services"},
		{name: "missingasset", reason: "open fixture asset", assetMissing: true},
		{
			name: "conflictingactivation", old: "setup:",
			replacement: "  second:\n    type: ext.report\n" +
				"    spec: {service: ext-svc, path: report.bin, token: ext-valid, config: {max_entries: 2, labels: [stable]}}\nsetup:",
			reason: "activation service already reserved",
		},
		{name: "nestedvalidator", old: "max_entries: 2, labels: [stable]", replacement: "max_entries: 1, labels: [stable, extra]", reason: "labels exceed max_entries"},
		{name: "format", old: "token: ext-valid", replacement: "token: wrong", reason: "invalid extension token"},
		{name: "missingreference", old: "report: report, on", replacement: "report: absent, on", reason: "resource"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := &externalCapability{}
			composition := externalComposition(t, c)
			body := externalScenarioYAML
			if test.old != "" {
				body = strings.ReplaceAll(body, test.old, test.replacement)
				require.NotEqual(t, externalScenarioYAML, body)
			}
			path, fixtures := externalFiles(t, body)
			if test.assetMissing {
				require.NoError(t, os.Remove(filepath.Join(fixtures, "report.bin")))
			}
			_, err := scenario.LoadScenario(t.Context(), path, scenario.LoadOptions{
				FixtureRoot: fixtures,
				Profiles:    []scenario.ProfileDeclaration{externalProfile()},
				Schema:      composition.Schema(),
			})
			require.ErrorIs(t, err, scenario.ErrInput)
			require.Contains(t, err.Error(), test.reason)
			require.Zero(t, c.acquisitions.Load())
			require.Zero(t, c.releases.Load())
		})
	}
	t.Run("runtimebindingdrift", func(t *testing.T) {
		c := &externalCapability{}
		_, err := boro.Compose(externalRevision, &externalDrift{externalCapability: c})
		require.ErrorIs(t, err, scenario.ErrInput)
		require.Contains(t, err.Error(), "binding changes an existing operation schema")
		require.Zero(t, c.acquisitions.Load())
	})
	t.Run("concretecontextdrift", func(t *testing.T) {
		c := &externalCapability{}
		composition := externalComposition(t, c)
		path, _ := externalFiles(t, externalScenarioYAML)
		document := externalLoad(t, path, composition)
		environment := externalEnvironment(document, "http://127.0.0.1:1")
		environment.Services[0].Capabilities = nil
		_, err := boro.Resolve(t.Context(), document, environment, composition)
		require.Error(t, err)
		require.Contains(t, err.Error(), "lacks capability")
		require.EqualValues(t, 2, c.validations.Load())
		require.Zero(t, c.acquisitions.Load())
	})
	t.Run("wrongresetactual", func(t *testing.T) {
		c := &externalCapability{}
		composition := externalComposition(t, c)
		baseline, err := composition.Schema().ReadResetBaseline(strings.NewReader(externalBaselineJSON))
		require.NoError(t, err)
		check := baseline.ExpectedChecks[0]
		require.Error(t, composition.Schema().ValidateObservedResetValue(check, externalResetExpected{Entries: []string{"seed"}}))
		require.NoError(t, composition.Schema().ValidateObservedResetValue(check, externalResetActual{Entries: []string{"seed"}}))
		require.ErrorContains(t, composition.Schema().ValidateObservedResetValue(check,
			externalResetActual{Entries: []string{"invalid"}}), "invalid extension inventory entry")
		for _, test := range []struct {
			actual string
			valid  bool
		}{{`{"entries":["seed"]}`, true}, {`{"entries":"seed"}`, false}} {
			data, err := externalReceipt(baseline, test.actual)
			require.NoError(t, err)
			receipt, err := boro.ReadResetReceipt(bytes.NewReader(data), baseline, composition.Schema())
			if test.valid {
				require.NoError(t, err)
				require.Equal(t, externalResetActual{Entries: []string{"seed"}}, receipt.Observations[0].Actual)
			} else {
				require.Error(t, err)
			}
		}
		require.Zero(t, c.acquisitions.Load())
	})
	t.Run("resetdeclarationcallbacks", func(t *testing.T) {
		c := &externalCapability{}
		composition := externalComposition(t, c)
		for _, test := range []struct{ name, old, replacement, reason string }{
			{"scope", `"scope":{"service":"ext-svc"}`, `"scope":{"service":"other"}`, "invalid extension reset scope"},
			{"domain", `"asset_ids":[]`, `"asset_ids":[],"mode":"direct"`, "unexpected extension reset assets or mode"},
			{"kind", `"kind":"ext.entries"`, `"kind":"ext.unknown"`, "unknown reset check"},
		} {
			t.Run(test.name, func(t *testing.T) {
				data := strings.Replace(externalBaselineJSON, test.old, test.replacement, 1)
				require.NotEqual(t, externalBaselineJSON, data)
				_, err := composition.Schema().ReadResetBaseline(strings.NewReader(data))
				require.ErrorContains(t, err, test.reason)
				require.Zero(t, c.acquisitions.Load())
			})
		}
	})
	t.Run("mutatedcallbackinput", func(t *testing.T) { externalIsolation(t) })
}

type (
	externalDrift      struct{ *externalCapability }
	externalDriftInput struct {
		Value string `yaml:"value"`
	}
)

func (d *externalDrift) Register(b boro.Bindings) (boro.Bindings, error) {
	return boro.BindAction(b, boro.ActionBinding[externalDriftInput]{
		Spec: scenario.OperationSpec{Name: "ext.apply", Capability: "ext", ResetDomain: externalDomain},
		NewPrepare: func() boro.ActionPreparer[externalDriftInput] {
			d.acquisitions.Add(1)
			return func(context.Context, *boro.RunScope, externalDriftInput) (boro.Action, error) {
				return nil, fmt.Errorf("unexpected drift preparation: %w", scenario.ErrInput)
			}
		},
	})
}

func externalReceipt(baseline scenario.ResetBaseline, actual string) ([]byte, error) {
	finished := time.Date(2026, 11, 13, 17, 0, 0, 0, time.UTC)
	started := finished.Add(time.Second)
	completed := started.Add(time.Second)
	return json.Marshal(boro.ResetReceipt{
		Version: 1, Identity: boro.ResultIdentity{
			InvocationID: "ext-invocation", ScenarioID: "ext.e2e",
			ScenarioHash: strings.Repeat("a", 64), ResolvedHash: strings.Repeat("b", 64),
			Registry: scenario.RegistryIdentity, RunnerImage: "sha256:" + strings.Repeat("d", 64), Project: "ext-project",
		},
		Generation: boro.GenerationIdentity{BatchID: "ext-batch", Generation: 1, Project: "ext-project", CompatibilityHash: strings.Repeat("e", 64)},
		Baseline:   boro.BaselineIdentity{ID: baseline.ID, SHA256: baseline.SHA256()},
		ResetID:    "ext-reset", Boundary: 1, CaseFinishedAt: finished, StartedAt: started, CompletedAt: completed,
		Observations: []boro.ResetObservation{{
			CheckID: "entries", Domain: externalDomain, Target: scenario.BoundResetTarget{Service: externalService, ContainerID: externalContainer},
			Outcome: scenario.ResetMatched, StartedAt: &started, CompletedAt: &completed, Actual: json.RawMessage(actual), ErrorIDs: []string{},
		}},
		Resources: []boro.ResetResource{}, Errors: []boro.ResetError{},
	})
}

func externalIsolation(t *testing.T) {
	t.Helper()
	var nested, resource, operation, asset, requirements int
	schema, err := scenario.WithTypeValidation(scenario.Schema{}, func(ctx scenario.ValidationContext, config externalConfig) error {
		nested++
		config.Labels[0] = "changed"
		ctx.Profile.Capabilities[0] = "changed"
		delete(ctx.Definition.Resources, "report")
		return nil
	})
	require.NoError(t, err)
	schema, err = schema.WithFormat("ext-token", func(string) error { return nil })
	require.NoError(t, err)
	schema, err = scenario.WithResource(schema, scenario.ResourceRegistration[externalReport]{
		Spec: scenario.ResourceSpec{Name: externalDomain, Capability: "ext"},
		Validate: func(ctx scenario.ValidationContext, spec externalReport) error {
			resource++
			if spec.Config.Labels[0] != "stable" || ctx.Profile.Capabilities[0] != "ext" || len(ctx.Definition.Resources) != 1 {
				return fmt.Errorf("nested callback leaked mutation: %w", scenario.ErrInput)
			}
			spec.Config.Labels[0] = "changed"
			ctx.Profile.Services[0].Name = "changed"
			delete(ctx.Definition.Resources, "report")
			return nil
		},
		Assets: func(spec externalReport) ([]scenario.AssetSource, error) {
			asset++
			if spec.Config.Labels[0] != "stable" {
				return nil, fmt.Errorf("resource callback leaked mutation: %w", scenario.ErrInput)
			}
			spec.Config.Labels[0] = "changed"
			return []scenario.AssetSource{{Path: spec.Path}}, nil
		},
		Requirements: func(spec externalReport) (scenario.ResourceRequirements, error) {
			requirements++
			if spec.Config.Labels[0] != "stable" {
				return scenario.ResourceRequirements{}, fmt.Errorf("asset callback leaked mutation: %w", scenario.ErrInput)
			}
			spec.Config.Labels[0] = "changed"
			return scenario.ResourceRequirements{Activations: []scenario.ServiceActivation{{Service: externalService, Activation: externalActivation}}}, nil
		},
	})
	require.NoError(t, err)
	observer := scenario.Observer[externalInput, externalWant](
		scenario.OperationSpec{Name: "ext.sample", Capability: "ext"},
		func(ctx scenario.ValidationContext, input externalInput, want externalWant) error {
			operation++
			spec, err := scenario.ResolveResource(ctx.Definition, input.Report)
			if err != nil {
				return err
			}
			if spec.Config.Labels[0] != "stable" || ctx.Profile.Services[0].Name != externalService {
				return fmt.Errorf("context callback leaked mutation: %w", scenario.ErrInput)
			}
			want.Entries[0] = "changed"
			ctx.Profile.Services[0].Capabilities[0] = "changed"
			spec.Config.Labels[0] = "changed"
			return nil
		},
	)
	schema, err = schema.WithDescriptor(observer)
	require.NoError(t, err)
	schema, err = schema.WithDescriptor(scenario.Action[externalInput](scenario.OperationSpec{Name: "ext.apply", Capability: "ext"}, nil))
	require.NoError(t, err)
	path, _ := externalFiles(t, externalScenarioYAML)
	document, err := scenario.LoadScenario(t.Context(), path, scenario.LoadOptions{
		FixtureRoot: filepath.Dir(path),
		Profiles:    []scenario.ProfileDeclaration{externalProfile()},
		Schema:      schema,
	})
	require.NoError(t, err)
	snapshot := document.Snapshot()
	profile := externalProfile()
	require.NoError(t, schema.ValidateDefinition(snapshot, profile, 2))
	spec, err := scenario.ResolveResource(snapshot, scenario.Ref[externalReport]("report"))
	require.NoError(t, err)
	require.Equal(t, []string{"stable"}, spec.Config.Labels)
	want, ok := snapshot.Steps[0].Want.(externalWant)
	require.True(t, ok)
	require.Equal(t, []string{"alpha", "beta"}, want.Entries)
	require.Equal(t, externalProfile(), profile)
	require.Equal(t, 2, nested)
	require.Equal(t, 2, resource)
	require.Equal(t, 2, operation)
	require.Equal(t, 1, asset)
	require.Equal(t, 2, requirements)
}

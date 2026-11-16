package boro

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	scen "github.com/pelageech/boro/scenario"
)

// ResourceKind distinguishes framework work from SUT fault state.
type ResourceKind string

const (
	ResourceClient ResourceKind = "client"
	ResourceHelper ResourceKind = "helper"
	ResourceFault  ResourceKind = "fault"
)

// ResourceIdentity describes exactly one acquired temporary resource.
type ResourceIdentity struct {
	ID      string       `json:"id"`
	Kind    ResourceKind `json:"kind"`
	Inspect string       `json:"inspect,omitempty"`
	Cleanup string       `json:"cleanup,omitempty"`
}

// TemporaryResource is registered immediately after acquisition.
// Stop cancels helpers without waiting. Release must join work and honor ctx,
// including after its deadline, before returning. It must not launch detached work.
type TemporaryResource struct {
	Identity ResourceIdentity
	Target   string
	Locator  ResetLocator
	Stop     context.CancelFunc
	Release  func(context.Context) error
}

// ResourceReceipt preserves ownership when release fails or a SUT fault is retained.
type ResourceReceipt struct {
	ResourceIdentity

	State string   `json:"state"`
	Error *Failure `json:"error,omitempty"`
}

// RunScope owns immutable inputs and temporary resources for exactly one run.
// Protocol factories create their clients here, never in a global cache.
type RunScope struct {
	definition  scen.Definition
	environment EnvironmentManifest
	assets      []scen.Asset
	mu          sync.Mutex
	resources   []TemporaryResource
	closed      bool
	onOwn       func(TemporaryResource) TemporaryResource
}

// Scenario supplies a detached snapshot to operation preparation.
func (s *RunScope) Scenario() scen.Definition { return scen.Clone(s.definition) }

// Environment supplies only this invocation's detached concrete bindings.
func (s *RunScope) Environment() EnvironmentManifest { return scen.Clone(s.environment) }

// Assets supplies the captured bytes hashed during loading.
func (s *RunScope) Assets() []scen.Asset { return scen.Clone(s.assets) }

// Own transfers a newly acquired resource to mandatory end-of-run cleanup.
func (s *RunScope) Own(resource TemporaryResource) error {
	if resource.Identity.ID == "" || resource.Release == nil {
		return inputError("ownership", "resource ID and release function are required")
	}
	switch resource.Identity.Kind {
	case ResourceHelper:
		if resource.Stop == nil {
			return inputError("ownership", "helpers require cancellation before joining")
		}
	case ResourceClient:
	case ResourceFault:
		if resource.Identity.Inspect == "" || resource.Identity.Cleanup == "" {
			return inputError("ownership", "faults require exact inspection and cleanup instructions")
		}
	default:
		return inputError("ownership", "unknown resource kind")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return inputError("ownership", "run has already joined and closed")
	}
	for _, existing := range s.resources {
		if existing.Identity.ID == resource.Identity.ID {
			return inputError("ownership", "duplicate resource ID "+resource.Identity.ID)
		}
	}
	if s.onOwn != nil {
		resource = s.onOwn(resource)
	}
	s.resources = append(s.resources, resource)
	return nil
}

func (s *RunScope) Close(ctx context.Context, retainFaults bool) ([]ResourceReceipt, error) {
	s.mu.Lock()
	s.closed = true
	resources := s.resources
	s.mu.Unlock()
	for _, resource := range resources {
		if resource.Stop != nil {
			resource.Stop()
		}
	}
	receipts := make([]ResourceReceipt, 0, len(resources))
	var failures []error
	for _, resource := range slices.Backward(resources) {
		receipt := ResourceReceipt{ResourceIdentity: resource.Identity, State: ResourceReleased}
		if resource.Identity.Kind == ResourceFault && retainFaults {
			receipt.State = resourceRetained
			receipts = append(receipts, receipt)
			continue
		}
		err := resource.Release(ctx)
		err = errors.Join(err, ctx.Err())
		if err != nil {
			err = fmt.Errorf("release %s: %w", resource.Identity.ID, err)
			receipt.State, receipt.Error = resourceFailed, failure("cleanup", err)
			failures = append(failures, err)
		}
		receipts = append(receipts, receipt)
	}
	return receipts, errors.Join(failures...)
}

// NewRunScope captures detached inputs for a set of prepared handlers. Its caller
// must Close it with an independent bounded context after all work has joined.
func NewRunScope(definition scen.Definition, environment EnvironmentManifest, assets []scen.Asset) *RunScope {
	return &RunScope{definition: scen.Clone(definition), environment: scen.Clone(environment), assets: scen.Clone(assets)}
}

func (r ResolvedScenario) NewRunScope() *RunScope {
	return NewRunScope(r.Snapshot(), r.Environment(), r.scenario.Assets())
}

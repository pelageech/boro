package boro

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"

	scen "boro/scenario"
)

// BoundService supplies concrete endpoints for a declared logical service.
type BoundService struct {
	Name         string            `json:"name"`
	Roles        []string          `json:"roles"`
	Capabilities []string          `json:"capabilities"`
	Families     []string          `json:"families"`
	Endpoints    map[string]string `json:"endpoints"`
	Peers        map[string]string `json:"peers,omitempty"`
	Interfaces   map[string]string `json:"interfaces"`
	Activation   string            `json:"activation,omitempty"`
}

// EnvironmentManifest supplies the typed portion needed for scenario binding.
// The environment provider owns allocation and the separate ownership ledger.
type EnvironmentManifest struct {
	Version      int               `json:"version"`
	InvocationID string            `json:"invocation_id"`
	ScenarioID   string            `json:"scenario_id"`
	ScenarioHash string            `json:"scenario_hash"`
	Profile      string            `json:"profile"`
	Project      string            `json:"project"`
	RunnerImage  string            `json:"runner_image"`
	Ownership    string            `json:"ownership,omitempty"`
	Addresses    map[string]string `json:"addresses"`
	Services     []BoundService    `json:"services"`
	Capabilities []string          `json:"capabilities"`
}

// ResolvedScenario holds a detached binding with an invocation-specific hash.
type ResolvedScenario struct {
	scenario    scen.Scenario
	definition  scen.Definition
	environment EnvironmentManifest
	hash        string
	registry    Bindings
}

// Snapshot returns bound typed inputs without mutable aliases.
func (r ResolvedScenario) Snapshot() scen.Definition { return scen.Clone(r.definition) }

// Hash identifies these exact bound inputs and invocation.
func (r ResolvedScenario) Hash() string { return r.hash }

// Schema returns the immutable declaration contract used for concrete binding.
func (r ResolvedScenario) Schema() scen.Schema { return r.registry.Schema() }

// Environment returns an isolated copy of the concrete binding.
func (r ResolvedScenario) Environment() EnvironmentManifest {
	return scen.Clone(r.environment)
}

// Resolve binds typed address references and checks concrete services before startup.
func Resolve(ctx context.Context, scenario scen.Scenario, environment EnvironmentManifest, composition Composition) (ResolvedScenario, error) {
	registry := composition.bindings
	if err := ctx.Err(); err != nil {
		return ResolvedScenario{}, fmt.Errorf("binding canceled: %w", err)
	}
	if environment.Version != 1 || environment.InvocationID == "" ||
		environment.ScenarioID != scenario.ID() || environment.ScenarioHash != scenario.Hash() ||
		environment.Profile != scenario.Snapshot().Environment.Profile {
		return ResolvedScenario{}, inputError("environment", "version, invocation, scenario ID/hash, and profile must match")
	}
	switch environment.Ownership {
	case "", "owned", environmentAttached:
	default:
		return ResolvedScenario{}, inputError("environment.ownership", "expected owned or attached")
	}
	actual, err := concreteProfile(scenario.Profile(), environment)
	if err != nil {
		return ResolvedScenario{}, err
	}
	definition := scenario.Snapshot()
	binder := addressBinding{profile: scenario.Profile(), addresses: environment.Addresses}
	if err := binder.walk(reflect.ValueOf(&definition).Elem()); err != nil {
		return ResolvedScenario{}, err
	}
	if err := scenario.ValidateBinding(definition, actual, registry.Schema()); err != nil {
		return ResolvedScenario{}, fmt.Errorf("resolve scenario: %w", scen.PrefixIssue("binding", err))
	}
	captured := scen.Clone(environment)
	encoded, err := json.Marshal(struct {
		ScenarioHash string
		Definition   scen.Definition
		Environment  EnvironmentManifest
	}{ScenarioHash: scenario.Hash(), Definition: definition, Environment: captured})
	if err != nil {
		return ResolvedScenario{}, fmt.Errorf("hash resolved input: %w", err)
	}
	return ResolvedScenario{scenario: scenario, definition: definition, environment: captured, hash: scen.Digest(encoded), registry: registry}, nil
}

func concreteProfile(declared scen.ProfileDeclaration, env EnvironmentManifest) (scen.ProfileDeclaration, error) {
	out := scen.ProfileDeclaration{Name: declared.Name, Addresses: declared.Addresses, Capabilities: slices.Clone(env.Capabilities)}
	for _, capability := range env.Capabilities {
		if !slices.Contains(declared.Capabilities, capability) {
			return scen.ProfileDeclaration{}, inputError("environment.capabilities", "undeclared capability "+capability)
		}
	}
	seen := make(map[string]bool)
	for _, bound := range env.Services {
		if seen[bound.Name] {
			return scen.ProfileDeclaration{}, inputError("environment.services", "duplicate service "+bound.Name)
		}
		seen[bound.Name] = true
		matches, err := declared.SelectServices(scen.Selector{Service: bound.Name}, "")
		if err != nil {
			return scen.ProfileDeclaration{}, fmt.Errorf("resolve scenario: %w", scen.PrefixIssue("environment.services", err))
		}
		original := matches[0]
		if bound.Activation != "" && bound.Activation != original.Activation {
			return scen.ProfileDeclaration{}, inputError("environment.services.activation", "unsupported deferred service activation")
		}
		for role, identity := range bound.Interfaces {
			if identity == "" || !slices.Contains(declared.Interfaces, role) {
				return scen.ProfileDeclaration{}, inputError("environment.services.interfaces", "empty or undeclared interface "+role)
			}
			if !slices.Contains(out.Interfaces, role) {
				out.Interfaces = append(out.Interfaces, role)
			}
		}
		for _, pair := range []struct {
			actual  []string
			allowed []string
		}{{bound.Roles, original.Roles}, {bound.Capabilities, original.Capabilities}, {bound.Families, original.Families}} {
			for _, value := range pair.actual {
				if !slices.Contains(pair.allowed, value) {
					return scen.ProfileDeclaration{}, inputError("environment.services", "undeclared role, capability, or family "+value)
				}
			}
		}
		out.Services = append(out.Services, scen.ServiceDeclaration{
			Name: bound.Name, Roles: bound.Roles, Capabilities: bound.Capabilities, Families: bound.Families, Activation: original.Activation,
		})
	}
	return out, nil
}

type addressBinding struct {
	profile   scen.ProfileDeclaration
	addresses map[string]string
}

func (b addressBinding) walk(v reflect.Value) error {
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		return b.indirect(v)
	}
	if v.Type() == scen.AddressType {
		address, ok := reflect.TypeAssert[scen.Address](v)
		if !ok {
			return inputError("binding", "invalid address type")
		}
		bound, err := b.address(address)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(bound))
		return nil
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				if err := b.walk(v.Field(i)); err != nil {
					return err
				}
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			if err := b.walk(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		for iter := v.MapRange(); iter.Next(); {
			value := reflect.New(v.Type().Elem()).Elem()
			value.Set(iter.Value())
			if err := b.walk(value); err != nil {
				return err
			}
			v.SetMapIndex(iter.Key(), value)
		}
	}
	return nil
}

func (b addressBinding) indirect(v reflect.Value) error {
	if v.IsNil() {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		return b.walk(v.Elem())
	}
	inner := reflect.New(v.Elem().Type()).Elem()
	inner.Set(v.Elem())
	if err := b.walk(inner); err != nil {
		return err
	}
	v.Set(inner)
	return nil
}

func (b addressBinding) address(address scen.Address) (scen.Address, error) {
	if address.Env == "" {
		return address, nil
	}
	family, err := b.profile.AddressFamily(address)
	if err != nil {
		return scen.Address{}, fmt.Errorf("resolve address family: %w", err)
	}
	literal := b.addresses[strings.TrimPrefix(address.Env, "addresses.")]
	ip, err := netip.ParseAddr(literal)
	if err != nil || ip.Zone() != "" || ip.Is4In6() || (ip.Is4() != (family == 4)) {
		return scen.Address{}, inputError(address.Env, "missing or wrong-family concrete environment address")
	}
	return scen.Address{Literal: literal}, nil
}

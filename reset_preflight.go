package boro

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/onsi/ginkgo/v2/types"

	scen "boro/scenario"
)

// PreflightReset performs selected-only validation without acquiring clients or
// changing services. Missing implementations never receive successful no-op handlers.
func PreflightReset(resolved ResolvedScenario, baseline scen.ResetBaseline, expected ResetContext, execution types.Report) error {
	if resolved.hash == "" || resolved.environment.Ownership != scen.ResetOwned {
		return inputError("reset", "reset requires a bound explicitly owned environment")
	}
	if err := ValidateSuiteReport(resolved, ModeFull, execution); err != nil {
		return err
	}
	if expected.CaseProcessExit != 0 || !expected.CaseHelpersJoined ||
		expected.CaseFinishedAt.Before(execution.EndTime) {
		return inputError("reset", "successful full execution and independently joined case process/helpers are required")
	}
	if err := validateResetContext(resolved, baseline, expected); err != nil {
		return err
	}
	domains, err := resolved.registry.ResetDomains(resolved.scenario)
	if err != nil {
		return err
	}
	for _, domain := range baseline.Domains {
		if !resolved.registry.ResetSupported(domain.Name) {
			return inputError("domains", "reset implementation unavailable: "+domain.Name)
		}
	}
	for _, domain := range domains {
		if !slices.ContainsFunc(baseline.Domains, func(row scen.ResetDomain) bool { return row.Name == domain }) {
			return inputError("domains", "selected action domain missing from baseline: "+domain)
		}
	}
	for _, domain := range baseline.Domains {
		if err := resolved.registry.resetHandlers[domain.Name].Validate(resolved, baseline); err != nil {
			return err
		}
	}
	return nil
}

func validateResetContext(resolved ResolvedScenario, baseline scen.ResetBaseline, expected ResetContext) error {
	data, err := json.Marshal(expected)
	if err != nil {
		return fmt.Errorf("encode reset context: %w", err)
	}
	if _, err := scen.StrictResetJSON[ResetContext](data); err != nil {
		return err
	}
	if err := validateResetCaseIdentity(expected.Identity); err != nil {
		return err
	}
	if err := baseline.ValidateCaptured(); err != nil {
		return fmt.Errorf("validate captured reset baseline: %w", err)
	}
	if err := resolved.registry.Schema().ValidateResetBaseline(baseline); err != nil {
		return fmt.Errorf("validate bound reset baseline: %w", err)
	}
	if expected.Identity != resolved.Identity() || expected.Generation.Project != resolved.environment.Project ||
		expected.Baseline != (BaselineIdentity{ID: baseline.ID, SHA256: baseline.SHA256()}) ||
		baseline.Profile != resolved.environment.Profile || baseline.Registry != resolved.Identity().Registry {
		return inputError("identity", "case, generation, profile, registry or baseline association differs")
	}
	targets := make(map[string]string)
	for _, target := range expected.BoundTargets {
		if targets[target.Service] != "" {
			return inputError("bound_targets", "duplicate service target")
		}
		service, err := BoundServiceByName(resolved.Environment(), target.Service)
		if err != nil {
			return err
		}
		if service.Endpoints["container_id"] != target.ContainerID {
			return inputError("bound_targets", "container differs from case binding")
		}
		targets[target.Service] = target.ContainerID
	}
	if len(targets) != len(resolved.environment.Services) {
		return inputError("bound_targets", "journal must bind every concrete case service")
	}
	return nil
}

func BoundServiceByName(environment EnvironmentManifest, name string) (BoundService, error) {
	for _, service := range environment.Services {
		if service.Name == name {
			return service, nil
		}
	}
	return BoundService{}, inputError("service", "unbound logical service "+name)
}

func resetResourceBinding(resolved ResolvedScenario, resource ResetResource) error {
	service, err := BoundServiceByName(resolved.Environment(), resource.Target.Service)
	if err != nil {
		return err
	}
	if resource.Target.ContainerID != service.Endpoints["container_id"] {
		return inputError("resource", "resource target differs from case container binding")
	}
	if resource.Kind == string(ResourceHelper) {
		for _, bound := range resolved.environment.Services {
			if resource.Locator.ContainerID == bound.Endpoints["container_id"] &&
				(resource.Locator.Type == scen.ResetContainer || (resource.Locator.PID != nil && *resource.Locator.PID == 1)) {
				return inputError("resource", "generation container cannot be a reset-local helper")
			}
		}
		return nil
	}
	for key, value := range service.Endpoints {
		if key == "container_id" || key == "peer_address" {
			continue
		}
		if value == resource.Locator.Endpoint || (strings.HasPrefix(value, "unix://") &&
			strings.TrimPrefix(value, "unix://") == resource.Locator.Endpoint) {
			return nil
		}
	}
	return inputError("resource", "client endpoint is not bound to its target service")
}

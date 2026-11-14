package boro

import (
	scen "boro/scenario"
)

// Mode declares whether checks run after setup.
type Mode string

const (
	ModeFull      Mode = "full"
	ModeConfigure Mode = "configure"
)

const (
	ResourceReleased    = "released"
	resourceRetained    = "retained"
	resourceFailed      = "failed"
	environmentAttached = "attached"
)

// ResultIdentity binds native report entries and owner records to immutable inputs.
type ResultIdentity struct {
	InvocationID string `json:"invocation_id"`
	ScenarioID   string `json:"scenario_id"`
	ScenarioHash string `json:"scenario_hash"`
	ResolvedHash string `json:"resolved_hash"`
	Registry     string `json:"registry"`
	RunnerImage  string `json:"runner_image"`
	Project      string `json:"project"`
}

func (r ResolvedScenario) Identity() ResultIdentity {
	return ResultIdentity{
		InvocationID: r.environment.InvocationID, ScenarioID: r.scenario.ID(),
		ScenarioHash: r.scenario.Hash(), ResolvedHash: r.Hash(), Registry: scen.RegistryIdentity,
		RunnerImage: r.environment.RunnerImage, Project: r.environment.Project,
	}
}

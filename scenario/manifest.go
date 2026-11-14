package scenario

import (
	"slices"
	"sort"
)

// Manifest is ordered offline selection evidence, never an execution result.
type Manifest struct {
	Version  int            `json:"version"`
	Registry string         `json:"registry"`
	Status   string         `json:"status"`
	Cases    []ManifestCase `json:"cases"`
}

// ManifestCase binds a selected identity to its scenario and fixture hashes.
type ManifestCase struct {
	ID                   string      `json:"id"`
	Source               string      `json:"source"`
	SourceSHA256         string      `json:"source_sha256"`
	ScenarioSHA256       string      `json:"scenario_sha256"`
	Profile              string      `json:"profile"`
	Tags                 []string    `json:"tags"`
	Assets               []Asset     `json:"assets"`
	Assertions           int         `json:"assertions"`
	Operations           []Operation `json:"operations"`
	RequiredCapabilities []string    `json:"required_capabilities"`
}

// Manifest returns deterministic selected metadata without volatile timestamps.
func (c Catalog) Manifest() Manifest {
	manifest := Manifest{Version: 1, Registry: RegistryIdentity, Status: "validated", Cases: make([]ManifestCase, 0, len(c.scenarios))}
	for _, scenario := range c.scenarios {
		registry := scenario.schema.entries()
		operations, assertions := Operations(scenario.definition)
		entry := ManifestCase{
			ID: scenario.ID(), Source: scenario.source, SourceSHA256: scenario.sourceHash, ScenarioSHA256: scenario.hash,
			Profile: scenario.definition.Environment.Profile, Tags: slices.Clone(scenario.definition.Tags),
			Assets: scenario.Assets(), Assertions: assertions, RequiredCapabilities: []string{}, Operations: []Operation{},
		}
		sort.Strings(entry.Tags)
		for _, name := range operations {
			capability := registry[name].Capability
			entry.Operations = append(entry.Operations, Operation{
				Name: name, Capability: capability, ExecutionAvailable: false, ResetDomain: registry[name].ResetDomain,
			})
			if !slices.Contains(entry.RequiredCapabilities, capability) {
				entry.RequiredCapabilities = append(entry.RequiredCapabilities, capability)
			}
		}
		sort.Strings(entry.RequiredCapabilities)
		manifest.Cases = append(manifest.Cases, entry)
	}
	return manifest
}

func Operations(d Definition) ([]string, int) {
	var names []string
	assertions := 0
	var visit func([]Step)
	visit = func(steps []Step) {
		for _, step := range steps {
			if step.Operation != "" && !slices.Contains(names, step.Operation) {
				names = append(names, step.Operation)
			}
			if step.Want != nil {
				assertions++
			}
			for _, branch := range step.Branches {
				visit(branch.Steps)
			}
		}
	}
	visit(d.Setup)
	visit(d.Steps)
	visit(d.Teardown)
	sort.Strings(names)
	return names, assertions
}

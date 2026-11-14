package scenario

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Selection combines exact case and tag selectors: OR within each, AND across groups.
type Selection struct {
	Cases []string
	Tags  []string
}

// LoadOptions supplies static profile declarations and the read-only fixture root.
// The default fixture root is the sibling fixtures directory of each catalog.
type LoadOptions struct {
	FixtureRoot     string
	ParallelActions int
	Profiles        []ProfileDeclaration
	Selection       Selection
	Schema          Schema
}

// Catalog contains validated, selected scenarios ordered by stable ID.
type Catalog struct{ scenarios []Scenario }

// Scenarios returns immutable scenario handles in stable ID order.
func (c Catalog) Scenarios() []Scenario { return slices.Clone(c.scenarios) }

// LoadCatalog discovers deterministic YAML files, validates all input, then selects.
func LoadCatalog(ctx context.Context, roots []string, options LoadOptions) (Catalog, error) {
	if len(roots) == 0 {
		return Catalog{}, invalid("catalog", "at least one catalog root is required")
	}
	discovery := catalogDiscovery{ctx: ctx, fixtures: make(map[string]string)}
	for _, root := range roots {
		if root == "" {
			return Catalog{}, invalid("catalog", "empty catalog root")
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return Catalog{}, fmt.Errorf("catalog root: %w", err)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return Catalog{}, fmt.Errorf("catalog %s: %w", root, err)
		}
		if !info.IsDir() {
			return Catalog{}, invalid("catalog", "catalog root must be a directory")
		}
		fixtureRoot := options.FixtureRoot
		if fixtureRoot == "" {
			fixtureRoot = filepath.Join(filepath.Dir(absolute), "fixtures")
		}
		discovery.fixtureRoot = fixtureRoot
		err = filepath.WalkDir(absolute, discovery.visit)
		if err != nil {
			return Catalog{}, fmt.Errorf("catalog %s: %w", root, err)
		}
	}
	sort.Strings(discovery.paths)
	scenarios := make([]Scenario, 0, len(discovery.paths))
	seen := make(map[string]string)
	for _, path := range discovery.paths {
		local := options
		local.FixtureRoot = discovery.fixtures[path]
		scenario, err := LoadScenario(ctx, path, local)
		if err != nil {
			return Catalog{}, err
		}
		if previous, exists := seen[scenario.ID()]; exists {
			return Catalog{}, &InputError{
				File: path, Line: 1, Column: 1, Field: "id", Category: "validation",
				Cause: invalid("id", "duplicate scenario ID also in "+previous),
			}
		}
		seen[scenario.ID()] = path
		scenarios = append(scenarios, scenario)
	}
	sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].ID() < scenarios[j].ID() })
	return selectScenarios(scenarios, options.Selection)
}

type catalogDiscovery struct {
	ctx         context.Context
	fixtureRoot string
	paths       []string
	fixtures    map[string]string
}

func (d *catalogDiscovery) visit(path string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return fmt.Errorf("discover %s: %w", path, walkErr)
	}
	if err := d.ctx.Err(); err != nil {
		return fmt.Errorf("discover canceled: %w", err)
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return invalid(path, "catalog symlinks are forbidden")
	}
	ext := strings.ToLower(filepath.Ext(path))
	if !entry.IsDir() && (ext == ".yaml" || ext == ".yml") {
		d.paths = append(d.paths, path)
		d.fixtures[path] = d.fixtureRoot
	}
	return nil
}

func selectScenarios(scenarios []Scenario, selection Selection) (Catalog, error) {
	ids, tags := make(map[string]bool), make(map[string]bool)
	for _, scenario := range scenarios {
		ids[scenario.ID()] = true
		for _, tag := range scenario.definition.Tags {
			tags[tag] = true
		}
	}
	for _, group := range []struct {
		field string
		keys  []string
		known map[string]bool
	}{{"case", selection.Cases, ids}, {"tag", selection.Tags, tags}} {
		for _, key := range group.keys {
			if !group.known[key] {
				return Catalog{}, invalid(group.field, "unknown or empty selector "+key)
			}
		}
	}
	var selected []Scenario
	for _, scenario := range scenarios {
		if len(selection.Cases) > 0 && !slices.Contains(selection.Cases, scenario.ID()) {
			continue
		}
		matches := len(selection.Tags) == 0
		for _, tag := range selection.Tags {
			matches = matches || slices.Contains(scenario.definition.Tags, tag)
		}
		if matches {
			selected = append(selected, scenario)
		}
	}
	if len(selected) == 0 {
		return Catalog{}, invalid("selection", "no scenarios selected")
	}
	return Catalog{scenarios: selected}, nil
}

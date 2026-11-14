package scenario

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
)

func (s Schema) ValidateDefinition(d Definition, p ProfileDeclaration, limit int) error {
	if d.Version != 1 {
		return invalid("version", "unsupported scenario version")
	}
	if !ValidName(d.ID) {
		return invalid("id", "expected lower-case letters, digits, dots, dashes, or underscores")
	}
	if err := uniqueStrings(d.Tags); err != nil {
		return PrefixIssue("tags", err)
	}
	t := treeValidation{definition: d, profile: p, schema: s, validateSelectors: true}
	if err := t.payload(d.Timeouts, "timeouts"); err != nil {
		return err
	}
	names := make([]string, 0, len(d.Resources))
	for name := range d.Resources {
		names = append(names, string(name))
	}
	sort.Strings(names)
	for _, name := range names {
		path := "resources." + name
		if !ValidName(name) {
			return invalid(path, "invalid resource name")
		}
		resource := d.Resources[Name(name)]
		descriptor, ok := s.LookupResource(resource.Type)
		if !ok || reflect.TypeOf(resource.Spec) != descriptor.Type {
			return invalid(path+".type", "unknown or mismatched resource type "+resource.Type)
		}
		required := descriptor.Capability
		if !slices.Contains(p.Capabilities, required) {
			return invalid(path+".type", "profile lacks capability "+required)
		}
		if err := t.validateResource(resource); err != nil {
			return PrefixIssue(path+".spec", err)
		}
		if descriptor.Validate != nil {
			if err := descriptor.Validate(Clone(ValidationContext{Definition: d, Profile: p}), Clone(resource.Spec)); err != nil {
				return PrefixIssue(path+".spec", err)
			}
		}
	}
	if err := s.validateRequirements(d, p); err != nil {
		return err
	}
	state := stepValidation{tree: t, limit: limit, steps: make(map[string]bool), branches: make(map[string]bool), registry: s}
	for _, group := range []struct {
		path  string
		steps []Step
	}{{SetupPhase, d.Setup}, {"steps", d.Steps}, {"teardown", d.Teardown}} {
		if err := state.walk(group.steps, group.path, false); err != nil {
			return err
		}
	}
	if state.assertions == 0 {
		return invalid("steps", "full execution requires at least one assertion")
	}
	return nil
}

type stepValidation struct {
	tree       treeValidation
	limit      int
	steps      map[string]bool
	branches   map[string]bool
	assertions int
	registry   Schema
}

func (v *stepValidation) walk(steps []Step, path string, nested bool) error {
	for i, step := range steps {
		field := fmt.Sprintf("%s[%d]", path, i)
		if err := v.step(step, field, nested); err != nil {
			return err
		}
	}
	return nil
}

func (v *stepValidation) step(step Step, field string, nested bool) error {
	if !ValidName(step.ID) || v.steps[step.ID] {
		return invalid(field+".id", "invalid or duplicate step ID")
	}
	v.steps[step.ID] = true
	if step.Timeout != nil && *step.Timeout <= 0 {
		return invalid(field+".timeout", "expected a positive Go duration")
	}
	if step.Branches != nil {
		return v.parallel(step, field, nested)
	}
	descriptor, ok := v.registry.Lookup(step.Operation)
	if !ok || reflect.TypeOf(step.Input) != descriptor.Input || reflect.TypeOf(step.Want) != descriptor.Want {
		return invalid(field, "unknown operation or registered payload type mismatch")
	}
	t := v.tree
	t.capability = descriptor.SelectorCapability
	t.validateSelectors = descriptor.ValidateSelectors
	if !slices.Contains(t.profile.Capabilities, descriptor.Capability) {
		return invalid(field, "profile lacks operation capability "+descriptor.Capability)
	}
	if step.Want != nil {
		if step.Wait == nil || step.Timeout != nil {
			return invalid(field+".wait", "observations require wait and forbid step timeout")
		}
		if len(field) >= 5 && field[:5] == SetupPhase {
			return invalid(field+".expect", "setup permits actions only")
		}
		v.assertions++
	} else if step.Wait != nil {
		return invalid(field+".wait", "actions cannot declare wait")
	}
	if err := t.payload(step.Input, field+".with"); err != nil {
		return err
	}
	if step.Want != nil {
		if err := t.payload(step.Want, field+".want"); err != nil {
			return err
		}
		if err := t.payload(step.Wait, field+".wait"); err != nil {
			return err
		}
	}
	if err := t.operation(step, descriptor); err != nil {
		return PrefixIssue(field, err)
	}
	if descriptor.Validate != nil {
		if err := descriptor.Validate(Clone(ValidationContext{Definition: t.definition, Profile: t.profile}), Clone(step)); err != nil {
			return PrefixIssue(field, err)
		}
	}
	return nil
}

func (v *stepValidation) parallel(step Step, field string, nested bool) error {
	if step.Operation != "" || step.Input != nil || step.Want != nil || step.Wait != nil || len(step.Branches) < 2 {
		return invalid(field+".parallel", "parallel blocks require at least two branches and forbid operation fields")
	}
	if nested {
		return invalid(field+".parallel", "nested parallel blocks are forbidden")
	}
	if len(field) >= 8 && field[:8] == "teardown" {
		return invalid(field+".parallel", "teardown parallel blocks are forbidden")
	}
	if len(step.Branches) > v.limit {
		return invalid(field+".parallel", "parallel block exceeds parallel-actions limit")
	}
	for j, branch := range step.Branches {
		branchPath := fmt.Sprintf("%s.parallel[%d]", field, j)
		if !ValidName(branch.ID) || v.branches[branch.ID] {
			return invalid(branchPath+".id", "invalid or duplicate branch ID")
		}
		v.branches[branch.ID] = true
		if len(branch.Steps) == 0 {
			return invalid(branchPath+".steps", "sequence must be nonempty")
		}
		if err := v.walk(branch.Steps, branchPath+".steps", true); err != nil {
			return err
		}
	}
	return nil
}

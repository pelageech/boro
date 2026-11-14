package scenario

import (
	"fmt"
)

func decodeSteps(wires []wireStep, path string, registry Schema) ([]Step, error) {
	out := make([]Step, 0, len(wires))
	for i, wire := range wires {
		step, err := wire.decode(fmt.Sprintf("%s[%d]", path, i), registry)
		if err != nil {
			return nil, err
		}
		out = append(out, step)
	}
	return out, nil
}

func (w wireStep) decode(field string, registry Schema) (Step, error) {
	variants := 0
	for _, present := range []bool{w.Do != nil, w.Expect != nil, w.Parallel != nil} {
		if present {
			variants++
		}
	}
	if variants != 1 {
		return Step{}, invalid(field, "exactly one of do, expect, or parallel is required")
	}
	if w.Parallel != nil {
		return w.decodeParallel(field, registry)
	}
	step := Step{ID: w.ID, Timeout: w.Timeout, Wait: w.Wait}
	discriminator := "do"
	if w.Do != nil {
		step.Operation = *w.Do
	}
	if w.Expect != nil {
		step.Operation, discriminator = *w.Expect, "expect"
	}
	if step.Operation == "" {
		return Step{}, invalid(field+"."+discriminator, "nonempty operation name is required")
	}
	reg, exists := registry.entries()[step.Operation]
	if !exists {
		return Step{}, invalid(field+"."+discriminator, "unknown operation "+step.Operation)
	}
	if (reg.Want != nil) != (w.Expect != nil) {
		return Step{}, invalid(field+"."+discriminator, "operation is used as the wrong step kind")
	}
	value, err := decodeRegistered(&w.With, reg.Input)
	if err != nil {
		return Step{}, PrefixIssue(field+".with", err)
	}
	step.Input = value
	switch discriminator {
	case "do":
		if w.Want.Kind != 0 || w.Wait != nil {
			return Step{}, invalid(field, "actions cannot declare want or wait")
		}
	case "expect":
		if w.Timeout != nil {
			return Step{}, invalid(field+".timeout", "observations use wait, not step timeout")
		}
		if w.Wait == nil {
			return Step{}, invalid(field+".wait", "observation wait is required")
		}
		value, err := decodeRegistered(&w.Want, reg.Want)
		if err != nil {
			return Step{}, PrefixIssue(field+".want", err)
		}
		step.Want = value
	}
	return step, nil
}

func (w wireStep) decodeParallel(field string, registry Schema) (Step, error) {
	if w.With.Kind != 0 || w.Want.Kind != 0 || w.Wait != nil {
		return Step{}, invalid(field, "parallel blocks permit only id, parallel, and timeout")
	}
	if len(w.Parallel) < 2 {
		return Step{}, invalid(field+".parallel", "at least two branches are required")
	}
	step := Step{ID: w.ID, Timeout: w.Timeout}
	for j, branch := range w.Parallel {
		steps, err := decodeSteps(branch.Steps, fmt.Sprintf("%s.parallel[%d].steps", field, j), registry)
		if err != nil {
			return Step{}, err
		}
		step.Branches = append(step.Branches, Branch{ID: branch.ID, Steps: steps})
	}
	return step, nil
}

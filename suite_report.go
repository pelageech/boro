package boro

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/onsi/ginkgo/v2/types"

	scen "boro/scenario"
)

// ReadSuiteReport reads the native Ginkgo JSON format for one foreground suite.
func ReadSuiteReport(reader io.Reader) (types.Report, error) {
	var reports []types.Report
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&reports); err != nil {
		return types.Report{}, fmt.Errorf("read Ginkgo report: %w", err)
	}
	if len(reports) != 1 {
		return types.Report{}, inputError("report", "exactly one foreground suite is required")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return types.Report{}, inputError("report", "trailing report content")
	}
	return reports[0], nil
}

// ValidateSuiteReport binds a native successful report to the complete selected
// YAML case. It does not reconstruct a custom result tree or infer owner cleanup.
func ValidateSuiteReport(resolved ResolvedScenario, mode Mode, report types.Report) error {
	if err := resolved.RequireExecution(mode); err != nil {
		return err
	}
	definition := resolved.Snapshot()
	if !report.SuiteSucceeded || report.SuiteDescription != definition.ID ||
		report.StartTime.IsZero() || report.EndTime.Before(report.StartTime) {
		return inputError("report", "a successful matching Ginkgo suite is required")
	}
	expected := map[string]bool{"prepare": true}
	groups := []struct {
		phase string
		steps []scen.Step
	}{{"setup", definition.Setup}, {"steps", definition.Steps}, {"teardown", definition.Teardown}}
	for _, group := range groups {
		if mode == ModeConfigure && group.phase != "setup" {
			continue
		}
		for _, step := range group.steps {
			expected[group.phase+"/"+step.ID] = true
		}
	}
	for _, spec := range report.SpecReports {
		if spec.State != types.SpecStatePassed {
			return inputError("report", "failed, skipped or pending work cannot establish successful execution")
		}
		if spec.LeafNodeType != types.NodeTypeIt {
			continue
		}
		if !expected[spec.LeafNodeText] || !slices.Contains(spec.Labels(), "mode:"+string(mode)) {
			return inputError("report", "duplicate or unexpected YAML step")
		}
		delete(expected, spec.LeafNodeText)
	}
	if len(expected) != 0 {
		return inputError("report", "incomplete YAML selection")
	}
	return validateSuiteMetadata(resolved, mode, report)
}

func validateSuiteMetadata(resolved ResolvedScenario, mode Mode, report types.Report) error {
	var identities, modes int
	for _, spec := range report.SpecReports {
		for _, entry := range spec.ReportEntries {
			switch entry.Name {
			case "boro.identity":
				identity, err := suiteEntry[ResultIdentity](entry)
				if err != nil || identity != resolved.Identity() {
					return inputError("report", "invocation or input identity differs")
				}
				identities++
			case "boro.mode":
				actual, err := suiteEntry[Mode](entry)
				if err != nil || actual != mode {
					return inputError("report", "execution mode differs")
				}
				modes++
			}
		}
	}
	if identities != 1 || modes != 1 {
		return inputError("report", "missing or duplicate invocation metadata")
	}
	return nil
}

func suiteEntry[T any](entry types.ReportEntry) (T, error) {
	var value T
	data, err := json.Marshal(entry.GetRawValue())
	if err != nil {
		return value, fmt.Errorf("encode Ginkgo report entry %s: %w", entry.Name, err)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("decode Ginkgo report entry %s: %w", entry.Name, err)
	}
	return value, nil
}

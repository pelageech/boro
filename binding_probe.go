package boro

import (
	"context"
	"errors"
	"time"

	scen "boro/scenario"
)

// BindingReport is protocol evidence, not a scenario execution or assertion.
type BindingReport struct {
	InvocationID string                       `json:"invocation_id"`
	Project      string                       `json:"project"`
	Observations []BindingObservation         `json:"observations"`
	Interfaces   map[string]map[string]string `json:"interfaces"`
	FinishedAt   time.Time                    `json:"finished_at"`
}

type BindingObservation struct {
	Service  string `json:"service"`
	Protocol string `json:"protocol"`
	Target   string `json:"target"`
	Actual   string `json:"actual"`
}

// ProbeBindings validates real protocol responses inside the persistent runner.
// Its clients belong to this one foreground process and are always released.
func ProbeBindings(ctx context.Context, manifest EnvironmentManifest, probe BindingProbe) (report BindingReport, err error) {
	report = BindingReport{
		InvocationID: manifest.InvocationID, Project: manifest.Project,
		Observations: []BindingObservation{}, Interfaces: make(map[string]map[string]string),
	}
	scope := NewRunScope(scen.Definition{}, manifest, nil)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, releaseErr := scope.Close(cleanup, false)
		err = errors.Join(err, releaseErr)
		report.FinishedAt = time.Now().UTC()
	}()
	for _, service := range manifest.Services {
		observations, interfaces, err := probe(ctx, scope, service)
		if err != nil {
			return report, err
		}
		report.Observations = append(report.Observations, observations...)
		if interfaces != nil {
			report.Interfaces[service.Name] = interfaces
		}
	}

	if len(report.Observations) == 0 {
		return report, inputError("binding", "no protocol observations")
	}
	return report, nil
}

type BindingProbe func(context.Context, *RunScope, BoundService) ([]BindingObservation, map[string]string, error)

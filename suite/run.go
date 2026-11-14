// Package suite compiles validated YAML scenarios into native Ginkgo specs.
// Each foreground runner process executes one suite. Scenario authors use YAML;
// capability packages supply the typed operations.
package suite

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	"github.com/onsi/gomega"

	"boro"
	scen "boro/scenario"
)

var (
	ErrFailed = errors.New("YAML suite failed")
	ErrUsed   = errors.New("a Ginkgo suite has already run in this process")
	started   atomic.Bool
)

// Options selects execution and bounds cleanup independently of cancellation.
type Options struct {
	Mode           boro.Mode
	CleanupTimeout time.Duration
	// Cleanup releases caller-owned resources as part of the native outcome.
	// It runs after invocation resources, even if their release fails.
	Cleanup func(context.Context) error
}

type testingT struct {
	failure error
}

func (t *testingT) Fail() { t.failure = ErrFailed }

// Run prepares the selected YAML tree and executes native ordered specs.
// Its report is Ginkgo's report, including skipped steps and cleanup failures.
// Ginkgo permits one suite per process; callers launch a fresh foreground
// runner for each invocation instead of resetting the framework's global state.
func Run(ctx context.Context, resolved boro.ResolvedScenario, options Options) (types.Report, error) {
	if err := resolved.RequireExecution(options.Mode); err != nil {
		return types.Report{}, fmt.Errorf("require YAML execution: %w", err)
	}
	if options.CleanupTimeout <= 0 {
		return types.Report{}, fmt.Errorf("positive cleanup timeout required: %w", scen.ErrInput)
	}
	if err := ctx.Err(); err != nil {
		return types.Report{}, fmt.Errorf("suite canceled before registration: %w", err)
	}
	if !started.CompareAndSwap(false, true) {
		return types.Report{}, ErrUsed
	}
	gomega.RegisterFailHandler(ginkgo.Fail)
	definition := resolved.Snapshot()
	ginkgo.Describe(definition.ID, ginkgo.Ordered, ginkgo.Label("mode:"+string(options.Mode)), func() {
		var scope *boro.RunScope
		var prepared map[string]boro.PreparedOperation
		var execution *stepExecution
		var failed bool
		ginkgo.AfterEach(func() { failed = failed || ginkgo.CurrentSpecReport().Failed() })
		ginkgo.BeforeAll(func() {
			ginkgo.AddReportEntry("boro.identity", resolved.Identity())
			ginkgo.AddReportEntry("boro.mode", options.Mode)
			runCtx, cancel := context.WithTimeout(ctx, time.Duration(definition.Timeouts.Scenario))
			ginkgo.DeferCleanup(func() error {
				cancel()
				cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), options.CleanupTimeout)
				defer cleanupCancel()
				var scopeErr, ownerErr error
				if scope != nil {
					retain := failed && resolved.Environment().Ownership != "attached"
					var resources []boro.ResourceReceipt
					resources, scopeErr = scope.Close(cleanupCtx, retain)
					ginkgo.AddReportEntry("boro.resources", resources, ginkgo.ReportEntryVisibilityFailureOrVerbose)
				}
				if options.Cleanup != nil {
					ownerErr = options.Cleanup(cleanupCtx)
				}
				if err := errors.Join(scopeErr, ownerErr, cleanupCtx.Err()); err != nil {
					return fmt.Errorf("close scenario and owner resources: %w", err)
				}
				return nil
			})
			execution = &stepExecution{
				ctx: runCtx, definition: definition,
				record: func(name string, value any) {
					ginkgo.AddReportEntry(name, value, ginkgo.ReportEntryVisibilityFailureOrVerbose)
				},
			}
		})
		ginkgo.It("prepare", func() {
			var err error
			scope, prepared, err = resolved.PrepareInvocation(execution.ctx, options.Mode)
			execution.prepared = prepared
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
		groups := []struct {
			phase string
			steps []scen.Step
		}{{"setup", definition.Setup}, {"steps", definition.Steps}, {"teardown", definition.Teardown}}
		for _, group := range groups {
			if options.Mode == boro.ModeConfigure && group.phase != "setup" {
				continue
			}
			for _, step := range group.steps {
				ginkgo.It(group.phase+"/"+step.ID, func() {
					detail, err := execution.step(execution.ctx, step)
					ginkgo.AddReportEntry("boro.operation", detail, ginkgo.ReportEntryVisibilityFailureOrVerbose)
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
				})
			}
		}
	})
	var report types.Report
	ginkgo.ReportAfterSuite("capture native report", func(actual types.Report) { report = actual })
	config := types.NewDefaultSuiteConfig()
	reporter := types.NewDefaultReporterConfig()
	reporter.NoColor = true
	reporter.Verbose = true
	runner := &testingT{}
	ginkgo.RunSpecs(runner, definition.ID, config, reporter)
	return report, runner.failure
}

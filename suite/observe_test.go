package suite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	runresult "github.com/pelageech/boro/result"

	"github.com/stretchr/testify/require"

	scen "github.com/pelageech/boro/scenario"

	"github.com/pelageech/boro"
)

var errObservation = errors.New("observation transport failed")

func observationFixture(t *testing.T, wait string, observer boro.Observer) (*stepExecution, scen.Step, *[]ObservationSample) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "case.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`version: 1
id: observation.timing
environment: {profile: test}
timeouts: {scenario: 1m, action: 10s, observation: 1s, poll: 1s}
resources: {}
steps:
  - id: check
    expect: test.status
    with: {on: {service: test-status}}
    want: {accept: [healthy]}
    wait: {`+wait+`}
`), 0o600))
	document, err := scen.LoadScenario(t.Context(), path, scen.LoadOptions{Profiles: testProfiles(), Schema: mustBuiltinSchema(t)})
	require.NoError(t, err)
	definition := document.Snapshot()
	var samples []ObservationSample
	e := &stepExecution{
		definition: definition,
		prepared:   map[string]boro.PreparedOperation{"check": {Observe: observer}},
		record: func(_ string, value any) {
			sample, ok := value.(ObservationSample)
			require.True(t, ok)
			samples = append(samples, sample)
		},
	}
	return e, definition.Steps[0], &samples
}

func TestObservationErrorsStopPolling(t *testing.T) {
	for _, mode := range []string{"mode: once", "mode: eventually, timeout: 10s", "mode: remains, duration: 10s"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			e, step, samples := observationFixture(t, mode, func(context.Context) (runresult.Observation, error) {
				calls++
				return runresult.Observation{}, errObservation
			})
			_, err := e.observe(t.Context(), step)
			require.ErrorIs(t, err, errObservation)
			require.NotErrorIs(t, err, runresult.ErrMismatch)
			require.Equal(t, 1, calls)
			require.Len(t, *samples, 1)
			require.Contains(t, (*samples)[0].Error, errObservation.Error())
		})
	}
}

func TestEventuallyDeadlineAndLastMismatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		e, step, samples := observationFixture(t, "mode: eventually, timeout: 2s", func(ctx context.Context) (runresult.Observation, error) {
			calls++
			if calls == 2 {
				<-ctx.Done()
				return runresult.Observation{}, ctx.Err()
			}
			return runresult.Observation{Detail: "value=0"}, nil
		})
		started := time.Now()
		_, err := e.observe(t.Context(), step)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, 2*time.Second, time.Since(started))
		require.Equal(t, 2, calls)
		require.Len(t, *samples, 2)
		require.Equal(t, "value=0", (*samples)[0].Observation.Detail)
		require.False(t, (*samples)[0].Observation.Matches)
		require.Contains(t, (*samples)[1].Error, context.DeadlineExceeded.Error())
	})
}

func TestObservationWindows(t *testing.T) {
	for _, test := range []struct {
		name, wait          string
		matchAt, mismatchAt int
		parent, elapsed     time.Duration
		calls               int
		expected            error
	}{
		{"converges", "mode: eventually, timeout: 5s", 3, 0, time.Minute, 2 * time.Second, 3, nil},
		{"eventual deadline", "mode: eventually, timeout: 3s", 0, 0, time.Minute, 3 * time.Second, 3, context.DeadlineExceeded},
		{"full sustained window", "mode: remains, duration: 5s, poll: 2s", 1, 0, time.Minute, 5 * time.Second, 3, nil},
		{"later mismatch", "mode: remains, duration: 5s", 1, 3, time.Minute, 2 * time.Second, 3, runresult.ErrMismatch},
		{"parent caps sustained window", "mode: remains, duration: 5s", 1, 0, 2 * time.Second, 2 * time.Second, 2, context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				e, step, samples := observationFixture(t, test.wait, func(context.Context) (runresult.Observation, error) {
					calls++
					matches := test.matchAt != 0 && calls >= test.matchAt
					if test.mismatchAt != 0 && calls >= test.mismatchAt {
						matches = false
					}
					return runresult.Observation{Matches: matches}, nil
				})
				ctx, cancel := context.WithTimeout(t.Context(), test.parent)
				defer cancel()
				started := time.Now()
				_, err := e.observe(ctx, step)
				require.Equal(t, test.elapsed, time.Since(started))
				require.Equal(t, test.calls, calls)
				require.Len(t, *samples, test.calls)
				if test.expected == nil {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, test.expected)
				}
			})
		})
	}
}

func TestCanceledObservationDoesNotDispatch(t *testing.T) {
	calls := 0
	e, step, samples := observationFixture(t, "mode: once", func(context.Context) (runresult.Observation, error) {
		calls++
		return runresult.Observation{Matches: true}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := e.observe(ctx, step)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls)
	require.Empty(t, *samples)
}

func TestRemainsRequiresObservation(t *testing.T) {
	for _, matches := range []bool{false, true} {
		t.Run(fmt.Sprint(matches), func(t *testing.T) {
			calls := 0
			e, step, samples := observationFixture(t, "mode: remains, duration: 1ns", func(context.Context) (runresult.Observation, error) {
				calls++
				return runresult.Observation{Matches: matches}, nil
			})
			_, err := e.observe(t.Context(), step)
			if matches {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, runresult.ErrMismatch)
			}
			require.Equal(t, 1, calls)
			require.Len(t, *samples, 1)
		})
	}
}

func TestRemainsJoinsWindowBoundaryObservation(t *testing.T) {
	for _, test := range []struct {
		name                   string
		delay, parent, elapsed time.Duration
		transport, expected    error
	}{
		{"completed", 800 * time.Millisecond, 10 * time.Second, 2200 * time.Millisecond, nil, nil},
		{"transport failure", 800 * time.Millisecond, 10 * time.Second, 2200 * time.Millisecond, errObservation, errObservation},
		{"observation deadline", 2 * time.Second, 10 * time.Second, 2400 * time.Millisecond, nil, context.DeadlineExceeded},
		{"parent deadline", 800 * time.Millisecond, 2 * time.Second, 2 * time.Second, nil, context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				e, step, samples := observationFixture(t, "mode: remains, duration: 2s", func(ctx context.Context) (runresult.Observation, error) {
					calls++
					delay := test.delay
					if calls == 1 {
						delay = 400 * time.Millisecond
					}
					timer := time.NewTimer(delay)
					defer timer.Stop()
					select {
					case <-ctx.Done():
						return runresult.Observation{}, ctx.Err()
					case <-timer.C:
					}
					if calls == 2 && test.transport != nil {
						return runresult.Observation{}, test.transport
					}
					return runresult.Observation{Matches: true}, nil
				})
				ctx, cancel := context.WithTimeout(t.Context(), test.parent)
				defer cancel()
				started := time.Now()
				_, err := e.observe(ctx, step)
				require.Equal(t, 2, calls)
				require.Equal(t, test.elapsed, time.Since(started))
				require.Len(t, *samples, 2)
				if test.expected == nil {
					require.NoError(t, err)
					for _, sample := range *samples {
						require.Empty(t, sample.Error)
						require.True(t, sample.Observation.Matches)
					}
				} else {
					require.ErrorIs(t, err, test.expected)
				}
			})
		})
	}
}

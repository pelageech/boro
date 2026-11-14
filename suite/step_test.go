package suite

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	runresult "boro/result"

	"github.com/stretchr/testify/require"

	"boro"
	scen "boro/scenario"
)

func TestStepDeadlineCannotBecomeLateSuccess(t *testing.T) {
	for _, test := range []struct {
		name                              string
		action, parent, override, elapsed time.Duration
	}{
		{"action bound", 200 * time.Millisecond, time.Minute, 0, 200 * time.Millisecond},
		{"parent caps action", time.Minute, 100 * time.Millisecond, 0, 100 * time.Millisecond},
		{"step overrides action", time.Minute, time.Minute, 200 * time.Millisecond, 200 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var completion ActionCompletion
				e := &stepExecution{
					definition: scen.Definition{Timeouts: scen.Timeouts{Action: scen.Duration(test.action)}},
					prepared: map[string]boro.PreparedOperation{"blocked": {
						Action: func(ctx context.Context) (runresult.Action, error) {
							<-ctx.Done()
							return runresult.Action{}, nil
						},
					}},
					record: func(_ string, value any) {
						actual, ok := value.(ActionCompletion)
						require.True(t, ok)
						completion = actual
					},
				}
				ctx, cancel := context.WithTimeout(t.Context(), test.parent)
				defer cancel()
				started := time.Now()
				step := scen.Step{ID: "blocked"}
				if test.override != 0 {
					override := scen.Duration(test.override)
					step.Timeout = &override
				}
				_, err := e.step(ctx, step)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Equal(t, test.elapsed, time.Since(started))
				require.Contains(t, completion.Error, context.DeadlineExceeded.Error())
			})
		})
	}
}

func TestParallelBlockDeadlineCapsBothBranches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var branches []BranchCompletion
		action := func(ctx context.Context) (runresult.Action, error) {
			<-ctx.Done()
			return runresult.Action{}, nil
		}
		e := &stepExecution{
			definition: scen.Definition{Timeouts: scen.Timeouts{Action: scen.Duration(time.Minute)}},
			prepared:   map[string]boro.PreparedOperation{"left": {Action: action}, "right": {Action: action}},
			record: func(_ string, value any) {
				if branch, ok := value.(BranchCompletion); ok {
					mu.Lock()
					branches = append(branches, branch)
					mu.Unlock()
				}
			},
		}
		step := parallelStep()
		timeout := scen.Duration(300 * time.Millisecond)
		step.Timeout = &timeout
		started := time.Now()
		err := e.parallel(t.Context(), step)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, 300*time.Millisecond, time.Since(started))
		require.Len(t, branches, 2)
		for _, branch := range branches {
			require.Contains(t, branch.Error, context.DeadlineExceeded.Error())
		}
	})
}

func TestParallelBlockStartsAndJoinsBothBranches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		var mu sync.Mutex
		var branches []BranchCompletion
		action := func(ctx context.Context) (runresult.Action, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return runresult.Action{}, nil
			case <-ctx.Done():
				return runresult.Action{}, ctx.Err()
			}
		}
		e := &stepExecution{
			definition: scen.Definition{Timeouts: scen.Timeouts{Action: scen.Duration(time.Minute)}},
			prepared:   map[string]boro.PreparedOperation{"left": {Action: action}, "right": {Action: action}},
			record: func(_ string, value any) {
				if branch, ok := value.(BranchCompletion); ok {
					mu.Lock()
					branches = append(branches, branch)
					mu.Unlock()
				}
			},
		}
		finished := make(chan error, 1)
		go func() { finished <- e.parallel(t.Context(), parallelStep()) }()
		<-entered
		<-entered
		synctest.Wait()
		select {
		case err := <-finished:
			close(release)
			t.Fatalf("parallel block returned before both branches joined: %v", err)
		default:
		}
		close(release)
		require.NoError(t, <-finished)
		require.Len(t, branches, 2)
		for _, branch := range branches {
			require.Empty(t, branch.Error)
			require.False(t, branch.FinishedAt.After(time.Now()))
		}
	})
}

func TestParallelFailureCancelsAndJoinsSibling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, joined := make(chan struct{}), make(chan struct{})
		e := &stepExecution{
			definition: scen.Definition{Timeouts: scen.Timeouts{Action: scen.Duration(time.Minute)}},
			prepared: map[string]boro.PreparedOperation{
				"left": {Action: func(ctx context.Context) (runresult.Action, error) {
					close(entered)
					<-ctx.Done()
					close(joined)
					return runresult.Action{}, ctx.Err()
				}},
				"right": {Action: func(context.Context) (runresult.Action, error) {
					<-entered
					return runresult.Action{}, errObservation
				}},
			},
			record: func(string, any) {},
		}
		err := e.parallel(t.Context(), parallelStep())
		require.ErrorIs(t, err, errObservation)
		require.ErrorIs(t, err, context.Canceled)
		select {
		case <-joined:
		default:
			t.Fatal("parallel block returned before its canceled sibling joined")
		}
		require.NotErrorIs(t, err, context.DeadlineExceeded)
	})
}

func parallelStep() scen.Step {
	return scen.Step{ID: "parallel", Branches: []scen.Branch{
		{ID: "left", Steps: []scen.Step{{ID: "left"}}},
		{ID: "right", Steps: []scen.Step{{ID: "right"}}},
	}}
}

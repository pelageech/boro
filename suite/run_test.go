package suite_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onsi/ginkgo/v2/types"
	"github.com/stretchr/testify/require"
)

// Each foreground invocation owns Ginkgo's one suite. The parent checks the
// native reports, including failures, rather than resetting framework globals.
func TestYAMLReports(t *testing.T) {
	if kind := os.Getenv("LABTEST_SUITE_CASE"); kind != "" {
		runChild(t, kind)
		return
	}
	for _, test := range []struct {
		name      string
		passed    bool
		states    []types.SpecState
		value     int
		mutations int
		samples   int
	}{
		{"success", true, []types.SpecState{types.SpecStatePassed, types.SpecStatePassed, types.SpecStatePassed, types.SpecStatePassed}, 0, 2, 1},
		{"setup-failure", false, []types.SpecState{types.SpecStatePassed, types.SpecStateFailed, types.SpecStateSkipped, types.SpecStateSkipped}, 0, 1, 0},
		{"observer-error", false, []types.SpecState{types.SpecStatePassed, types.SpecStatePassed, types.SpecStateFailed, types.SpecStateSkipped}, 7, 1, 1},
		{"cleanup-failure", false, []types.SpecState{types.SpecStatePassed, types.SpecStatePassed, types.SpecStatePassed, types.SpecStateFailed}, 0, 2, 1},
		{"preparation-failure", false, []types.SpecState{types.SpecStateFailed, types.SpecStateSkipped, types.SpecStateSkipped, types.SpecStateSkipped}, 0, 0, 0},
		{"configure", true, []types.SpecState{types.SpecStatePassed, types.SpecStatePassed}, 7, 1, 0},
		{"owned-fault", false, []types.SpecState{types.SpecStatePassed, types.SpecStateFailed, types.SpecStateSkipped, types.SpecStateSkipped}, 0, 1, 0},
		{"attached-fault", false, []types.SpecState{types.SpecStatePassed, types.SpecStateFailed, types.SpecStateSkipped, types.SpecStateSkipped}, 0, 1, 0},
		{"helper-joined", false, []types.SpecState{types.SpecStatePassed, types.SpecStateFailed, types.SpecStateSkipped, types.SpecStateSkipped}, 0, 1, 0},
		{"owner-cleanup-failure", false, []types.SpecState{types.SpecStatePassed, types.SpecStatePassed, types.SpecStatePassed, types.SpecStateFailed}, 0, 2, 1},
		{"scenario-deadline", false, []types.SpecState{types.SpecStatePassed, types.SpecStateFailed, types.SpecStateSkipped, types.SpecStateSkipped}, 0, 1, 0},
		{"cleanup-timeout", false, []types.SpecState{types.SpecStatePassed, types.SpecStatePassed, types.SpecStatePassed, types.SpecStateFailed}, 0, 2, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			executable, err := os.Executable()
			require.NoError(t, err)
			command := exec.CommandContext(t.Context(), executable, "-test.run=^TestYAMLReports$", "-test.v")
			command.Env = append(os.Environ(), "LABTEST_SUITE_CASE="+test.name, "LABTEST_SUITE_OUTPUT="+root)
			output, runErr := command.CombinedOutput()
			if test.passed {
				require.NoError(t, runErr, "%s", output)
			} else {
				var exit *exec.ExitError
				require.ErrorAs(t, runErr, &exit, "%s", output)
			}
			data, err := os.ReadFile(filepath.Join(root, "report.json"))
			require.NoError(t, err, "%s", output)
			var reports []types.Report
			require.NoError(t, json.Unmarshal(data, &reports))
			require.Len(t, reports, 1)
			require.Equal(t, test.passed, reports[0].SuiteSucceeded)
			var states []types.SpecState
			for _, spec := range reports[0].SpecReports {
				if spec.LeafNodeType == types.NodeTypeIt {
					states = append(states, spec.State)
				}
			}
			require.Equal(t, test.states, states, "%s", output)
			stateData, err := os.ReadFile(filepath.Join(root, "counter.json"))
			require.NoError(t, err)
			var state counterState
			require.NoError(t, json.Unmarshal(stateData, &state))
			require.Equal(t, test.value, state.Value)
			require.Equal(t, test.mutations, state.Mutations)
			require.Equal(t, test.samples, state.Samples)
			require.Equal(t, 1, state.Cleanups)
			require.Equal(t, 1, state.OwnerCleanups)
			require.Equal(t, test.name == "attached-fault", state.FaultRestored)
			require.Equal(t, test.name == "helper-joined", state.HelperJoined)
		})
	}
}

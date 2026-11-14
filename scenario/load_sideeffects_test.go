package scenario_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func recordSideEffects(t *testing.T) {
	t.Helper()
	var dockerCalls atomic.Uint64
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dockerCalls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	bin := t.TempDir()
	processLog := filepath.Join(bin, "process-calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"$SUT_SIDE_EFFECT_LOG\"\nexit 79\n"
	for _, name := range []string{"docker", "docker-compose", "gobgp", "vppctl", "adjuster", "adjustermon", "lab-arp"} {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700))
	}
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(docker.URL, "http://"))
	t.Setenv("SUT_SIDE_EFFECT_LOG", processLog)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		docker.Close()
		require.Zero(t, dockerCalls.Load(), "invalid input must not call Docker")
		_, err := os.Stat(processLog)
		require.ErrorIs(t, err, os.ErrNotExist, "invalid input must not invoke SUT processes")
	})
}

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

package counter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"boro/examples/counter"
)

func TestMemoryBackendOwnsValuesSeparatelyFromHandles(t *testing.T) {
	backend := counter.NewMemoryBackend()
	first, err := counter.New(t.Context(), counter.Spec{Name: "requests"}, backend)
	require.NoError(t, err)
	second, err := counter.New(t.Context(), counter.Spec{Name: "requests"}, backend)
	require.NoError(t, err)
	require.NoError(t, first.Add(t.Context(), 7))
	require.NoError(t, first.Close(t.Context()))
	_, err = first.Value(t.Context())
	require.ErrorIs(t, err, counter.ErrClosed)
	value, err := second.Value(t.Context())
	require.NoError(t, err)
	require.Equal(t, 7, value)
	require.NoError(t, second.Close(t.Context()))

	isolated, err := counter.New(t.Context(), counter.Spec{Name: "requests"}, counter.NewMemoryBackend())
	require.NoError(t, err)
	value, err = isolated.Value(t.Context())
	require.NoError(t, err)
	require.Zero(t, value)
	require.NoError(t, isolated.Close(t.Context()))
}

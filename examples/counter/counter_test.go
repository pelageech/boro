package counter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pelageech/boro"
	"github.com/pelageech/boro/examples/counter"
	"github.com/pelageech/boro/scenario"
)

func TestCounterGoAndYAMLShareBehavior(t *testing.T) {
	backend := counter.NewMemoryBackend()
	direct, err := counter.New(t.Context(), counter.Spec{Name: "requests"}, backend)
	require.NoError(t, err)
	fromYAML, err := counter.FromYAML(t.Context(), []byte("name: requests\n"), backend)
	require.NoError(t, err)
	require.NoError(t, direct.Add(t.Context(), 2))
	value, err := fromYAML.Value(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, value)
	require.NoError(t, fromYAML.Add(t.Context(), 3))
	value, err = direct.Value(t.Context())
	require.NoError(t, err)
	require.Equal(t, 5, value)
	require.ErrorIs(t, direct.Add(t.Context(), 0), scenario.ErrInput)
	require.NoError(t, direct.Close(t.Context()))
	require.NoError(t, fromYAML.Close(t.Context()))
}

type backendFunc func(context.Context, counter.Spec) (counter.Client, error)

func (f backendFunc) Open(ctx context.Context, spec counter.Spec) (counter.Client, error) {
	return f(ctx, spec)
}

func TestCounterRejectsSpecBeforeOpening(t *testing.T) {
	backend := backendFunc(func(context.Context, counter.Spec) (counter.Client, error) {
		t.Fatal("invalid spec opened a backend")
		return nil, nil
	})
	_, err := counter.New(t.Context(), counter.Spec{Name: "Invalid Name"}, backend)
	require.ErrorIs(t, err, scenario.ErrInput)
}

func TestCounterClosesPartialAcquisitionAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	b := &backend{partial: true}
	provider := backendFunc(func(ctx context.Context, spec counter.Spec) (counter.Client, error) {
		be, err := b.factory(ctx, boro.EnvironmentManifest{})
		if err != nil {
			cancel()
			return nil, err
		}
		client, err := be.Open(ctx, spec)
		cancel()
		return client, err
	})
	c, err := counter.New(ctx, counter.Spec{Name: "requests"}, provider)
	require.Nil(t, c)
	require.ErrorIs(t, err, errClient)
	require.Equal(t, 1, b.created)
	require.Equal(t, 1, b.closed, "partial handle must close with a fresh bounded context")
}

func TestCounterRejectsMissingClient(t *testing.T) {
	for _, test := range []struct {
		name    string
		backend counter.Backend
	}{
		{"nil backend", nil},
		{"empty acquisition", backendFunc(func(context.Context, counter.Spec) (counter.Client, error) { return nil, nil })},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := counter.New(t.Context(), counter.Spec{Name: "requests"}, test.backend)
			require.ErrorIs(t, err, scenario.ErrInput)
		})
	}
}

func TestCounterPreservesBackendErrors(t *testing.T) {
	failure := errors.New("backend unavailable")
	_, err := counter.New(t.Context(), counter.Spec{Name: "requests"},
		backendFunc(func(context.Context, counter.Spec) (counter.Client, error) { return nil, failure }))
	require.ErrorIs(t, err, failure)
}

func TestFromYAMLRejectsInvalidSpecsBeforeAcquisition(t *testing.T) {
	for _, test := range []struct{ name, yaml, diagnostic string }{
		{"unknown field", "name: requests\nextra: true", "unknown field"},
		{"missing field", "{}", "required field"},
		{"wrong scalar", "name: 42", "expected str"},
		{"invalid name", "name: Invalid Name", "identifier"},
		{"duplicate", "name: requests\nname: other", "duplicate key"},
		{"null", "name: null", "null is forbidden"},
		{"anchor", "name: &name requests", "anchors and aliases"},
		{"alias", "name: *name", "unknown anchor"},
		{"extra document", "name: requests\n---\nname: other", "trailing YAML"},
		{"empty document", "", "decode YAML: EOF"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := backendFunc(func(context.Context, counter.Spec) (counter.Client, error) {
				t.Fatal("invalid YAML opened a backend")
				return nil, nil
			})
			_, err := counter.FromYAML(t.Context(), []byte(test.yaml), backend)
			require.ErrorContains(t, err, test.diagnostic)
		})
	}
}

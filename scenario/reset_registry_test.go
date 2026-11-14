package scenario_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	scen "boro/scenario"
)

type extensionResetScope struct {
	Names []string `json:"names" format:"id"`
}

type extensionResetExpected struct {
	Values []uint32 `json:"values" max:"9"`
}

type extensionResetActual struct {
	Values []uint32 `json:"values" max:"9"`
}

type recursiveResetScope struct {
	Next *recursiveResetScope `json:"next,omitempty"`
}

func extensionResetRegistration() scen.ResetCheckRegistration[extensionResetScope, extensionResetExpected, extensionResetActual] {
	return scen.ResetCheckRegistration[extensionResetScope, extensionResetExpected, extensionResetActual]{
		Kind: "extension.values", Domains: []string{"extension.domain"},
		Validate: func(ctx scen.ResetValidationContext, scope extensionResetScope, expected extensionResetExpected) error {
			if ctx.Profile != "extension" || ctx.Target.Service != "reserved" ||
				len(scope.Names) != 1 || scope.Names[0] != "counter" || len(expected.Values) != 1 || expected.Values[0] != 7 {
				return errors.New("extension reset context rejected")
			}
			scope.Names[0], expected.Values[0] = "mutated", 9
			ctx.Assets[0].Purpose = "mutated"
			return nil
		},
		ValidateActual: func(actual extensionResetActual) error {
			if len(actual.Values) != 1 {
				return errors.New("complete extension inventory required")
			}
			actual.Values[0] = 9
			return nil
		},
		Compare: func(expected extensionResetExpected, actual extensionResetActual) (bool, error) {
			matches := reflect.DeepEqual(expected.Values, actual.Values)
			expected.Values[0], actual.Values[0] = 9, 9
			return matches, nil
		},
	}
}

func extensionResetSchema(t *testing.T) scen.Schema {
	t.Helper()
	schema, err := (scen.Schema{}).WithResetDomain(scen.ResetDomainRegistration{
		Name: "extension.domain",
		Validate: func(b scen.ResetBaseline, domain scen.ResetDomain) error {
			if b.Profile != "extension" || domain.Mode != "custom" || len(domain.AssetIDs) != 1 ||
				b.Assets[0].Purpose != "extension.input" {
				return errors.New("extension domain policy rejected")
			}
			b.ExpectedChecks[0].Expected = extensionResetExpected{Values: []uint32{9}}
			domain.AssetIDs[0] = "mutated"
			return nil
		},
	})
	require.NoError(t, err)
	schema, err = schema.WithResetProfile(scen.ResetProfilePolicy{
		Profile: "extension", RequiredDomains: []string{"extension.domain"}, AllowedDomains: []string{"extension.domain"},
	})
	require.NoError(t, err)
	schema, err = scen.WithResetCheck(schema, extensionResetRegistration())
	require.NoError(t, err)
	return schema
}

func extensionResetJSON() string {
	return `{"version":1,"id":"extension-baseline","profile":"extension","registry":"boro.v1",
"domains":[{"name":"extension.domain","asset_ids":["input"],"mode":"custom"}],
"assets":[{"id":"input","path":"input.txt","sha256":"` + strings.Repeat("a", 64) + `","size_bytes":0,
"service":"reserved","purpose":"extension.input","target_path":"/input.txt"}],
"expected_checks":[{"id":"values","domain":"extension.domain","target":{"service":"reserved"},
"kind":"extension.values","scope":{"names":["counter"]},"expected":{"values":[7]}}]}`
}

func TestResetRegistrations(t *testing.T) {
	schema := extensionResetSchema(t)
	wire := extensionResetJSON()
	baseline, err := schema.ReadResetBaseline(strings.NewReader(wire))
	require.NoError(t, err)
	require.Equal(t, scen.Digest([]byte(wire)), baseline.SHA256())
	require.Equal(t, extensionResetScope{Names: []string{"counter"}}, baseline.ExpectedChecks[0].Scope)
	require.Equal(t, extensionResetExpected{Values: []uint32{7}}, baseline.ExpectedChecks[0].Expected)
	require.NoError(t, baseline.ValidateCaptured())
	before := scen.Clone(baseline)
	require.NoError(t, schema.ValidateResetBaseline(baseline))
	require.Equal(t, before, baseline)
	check := baseline.ExpectedChecks[0]
	actual := extensionResetActual{Values: []uint32{7}}
	require.NoError(t, schema.ValidateObservedResetValue(check, actual))
	match, err := schema.CompareResetValue(check, actual)
	require.NoError(t, err)
	require.True(t, match)
	require.Equal(t, extensionResetActual{Values: []uint32{7}}, actual)
	require.Equal(t, before.ExpectedChecks[0], check)
	match, err = schema.CompareResetValue(check, extensionResetActual{Values: []uint32{8}})
	require.NoError(t, err)
	require.False(t, match)
	other, err := schema.ReadResetBaseline(strings.NewReader(wire + "\n"))
	require.NoError(t, err)
	require.NotEqual(t, baseline.SHA256(), other.SHA256())
	baseline.ExpectedChecks[0].Expected = extensionResetExpected{Values: []uint32{8}}
	require.ErrorContains(t, baseline.ValidateCaptured(), "immutable")
}

func TestResetRegistrationRejections(t *testing.T) {
	schema := extensionResetSchema(t)
	for _, tc := range []struct{ name, before, after, diagnostic string }{
		{"unknown kind", "extension.values", "extension.unknown", "unknown reset check"},
		{"unknown domain", "extension.domain", "extension.unknown", "unknown reset domain"},
		{"unknown profile", `"profile":"extension"`, `"profile":"unknown"`, "unknown reset profile"},
		{"wrong scope", `"names":["counter"]`, `"other":["counter"]`, "names"},
		{"null expected", `"expected":{"values":[7]}`, `"expected":null`, "null"},
		{"context mismatch", `"service":"reserved"`, `"service":"other"`, "context rejected"},
		{"semantic expected", `"values":[7]`, `"values":[8]`, "context rejected"},
		{"scalar constraint", `"values":[7]`, `"values":[10]`, "max 9"},
		{"duplicate key", `"version":1`, `"version":1,"version":1`, "duplicate"},
		{"unknown field", `"version":1`, `"version":1,"execute":"touch /tmp/injected"`, "unknown"},
		{"missing asset reference", `"asset_ids":["input"]`, `"asset_ids":["missing"]`, "missing or multiply"},
		{"path escape", `"path":"input.txt"`, `"path":"../input.txt"`, "contained"},
		{"domain policy", `"mode":"custom"`, `"mode":"other"`, "domain policy rejected"},
		{"empty checks", `"expected_checks":[{"id":"values","domain":"extension.domain","target":{"service":"reserved"},
"kind":"extension.values","scope":{"names":["counter"]},"expected":{"values":[7]}}]`, `"expected_checks":[]`, "nonempty complete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := extensionResetJSON()
			require.Contains(t, wire, tc.before)
			_, err := schema.ReadResetBaseline(strings.NewReader(strings.ReplaceAll(wire, tc.before, tc.after)))
			require.ErrorContains(t, err, tc.diagnostic)
		})
	}
	baseline, err := schema.ReadResetBaseline(strings.NewReader(extensionResetJSON()))
	require.NoError(t, err)
	t.Run("native types and field constraints", func(t *testing.T) {
		check := baseline.ExpectedChecks[0]
		require.ErrorContains(t, schema.ValidateObservedResetValue(check, extensionResetExpected{Values: []uint32{7}}), "wrong concrete type")
		_, err := schema.CompareResetValue(check, extensionResetExpected{Values: []uint32{7}})
		require.ErrorContains(t, err, "wrong concrete type")
		require.ErrorContains(t, schema.ValidateObservedResetValue(check, extensionResetActual{Values: []uint32{10}}), "max 9")
		require.ErrorContains(t, schema.ValidateObservedResetValue(check, extensionResetActual{Values: []uint32{}}), "complete extension inventory")
		native := scen.Clone(baseline)
		native.ExpectedChecks[0].Scope = scen.EmptyScope{}
		require.ErrorContains(t, schema.ValidateResetBaseline(native), "wrong concrete scope")
		native = scen.Clone(baseline)
		native.ExpectedChecks[0].Expected = extensionResetActual{Values: []uint32{7}}
		require.ErrorContains(t, schema.ValidateResetBaseline(native), "wrong concrete scope or expected")
	})
	t.Run("registration rejects conflicts and missing contracts", func(t *testing.T) {
		entry := extensionResetRegistration()
		_, err := scen.WithResetCheck(schema, entry)
		require.ErrorContains(t, err, "already registered")
		entry.Kind = "extension.other"
		entry.Compare = nil
		_, err = scen.WithResetCheck(schema, entry)
		require.ErrorContains(t, err, "complete callbacks")
		entry = extensionResetRegistration()
		entry.Kind, entry.Domains = "extension.other", []string{"missing"}
		_, err = scen.WithResetCheck(schema, entry)
		require.ErrorContains(t, err, "undeclared reset domain")
		_, err = schema.WithResetDomain(scen.ResetDomainRegistration{Name: "missing"})
		require.ErrorContains(t, err, "validator")
		_, err = schema.WithResetProfile(scen.ResetProfilePolicy{Profile: "missing", AllowedDomains: []string{"missing"}})
		require.ErrorContains(t, err, "undeclared reset policy domain")
		_, err = schema.WithResetProfile(scen.ResetProfilePolicy{Profile: "other", AllowedDomains: []string{"extension.domain"}, RequiredDomains: []string{"missing"}})
		require.ErrorContains(t, err, "not allowed")
		_, err = schema.WithResetProfile(scen.ResetProfilePolicy{Profile: "extension", AllowedDomains: []string{"extension.domain"}})
		require.ErrorContains(t, err, "already registered")
	})
	t.Run("unsupported reset shapes fail at registration", func(t *testing.T) {
		_, err := scen.WithResetCheck(schema, scen.ResetCheckRegistration[recursiveResetScope, extensionResetExpected, extensionResetActual]{
			Kind: "extension.recursive", Domains: []string{"extension.domain"},
			Validate:       func(scen.ResetValidationContext, recursiveResetScope, extensionResetExpected) error { return nil },
			ValidateActual: extensionResetRegistration().ValidateActual, Compare: extensionResetRegistration().Compare,
		})
		require.ErrorContains(t, err, "recursive reset shapes")
		_, err = scen.WithResetCheck(schema, scen.ResetCheckRegistration[scen.EmptyScope, any, extensionResetActual]{
			Kind: "extension.dynamic", Domains: []string{"extension.domain"},
			Validate:       func(scen.ResetValidationContext, scen.EmptyScope, any) error { return nil },
			ValidateActual: extensionResetRegistration().ValidateActual,
			Compare:        func(any, extensionResetActual) (bool, error) { return false, nil },
		})
		require.ErrorContains(t, err, "concrete structs")
	})
	t.Run("derived registry cannot mutate original", func(t *testing.T) {
		_, _, _, err := (scen.Schema{}).ResetCheckTypes("extension.values")
		require.ErrorContains(t, err, "unknown reset check")
		data, err := json.Marshal(baseline)
		require.NoError(t, err)
		_, err = (scen.Schema{}).ReadResetBaseline(bytes.NewReader(data))
		require.Error(t, err)
	})
}

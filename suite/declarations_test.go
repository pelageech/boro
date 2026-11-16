package suite

import (
	"testing"

	"github.com/pelageech/boro/scenario"
)

type (
	testProbe struct {
		On scenario.Selector `yaml:"on"`
	}
	testStatus struct {
		Accept []string `yaml:"accept"`
	}
)

func mustBuiltinSchema(t *testing.T) scenario.Schema {
	t.Helper()
	schema, err := (scenario.Schema{}).WithDescriptor(scenario.Observer[testProbe, testStatus](scenario.OperationSpec{Name: "test.status", Capability: "test"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func testProfiles() []scenario.ProfileDeclaration {
	return []scenario.ProfileDeclaration{{Name: "test", Capabilities: []string{"test"}, Services: []scenario.ServiceDeclaration{{Name: "test-status", Roles: []string{"test-status"}, Capabilities: []string{"test"}}}}}
}

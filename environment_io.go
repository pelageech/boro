package boro

import (
	"io"

	scen "github.com/pelageech/boro/scenario"
)

// ReadEnvironment parses exactly one strict environment document.
func ReadEnvironment(reader io.Reader) (EnvironmentManifest, error) {
	return scen.ReadJSON[EnvironmentManifest](reader)
}

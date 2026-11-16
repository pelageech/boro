package command

import (
	"context"
	"time"

	"github.com/pelageech/boro"
	"github.com/pelageech/boro/scenario"
)

type BaselineCapture func(context.Context, boro.ResolvedScenario, string, time.Duration, time.Duration) ([]byte, error)

// Options explicitly supplies the compiled capabilities of this runner.
// Probe and Capture enable their corresponding commands when provided.
type Options struct {
	// Composition keeps declarations, execution bindings and revision together.
	Composition boro.Composition
	Profiles    []scenario.ProfileDeclaration
	Probe       boro.BindingProbe
	Capture     BaselineCapture
}

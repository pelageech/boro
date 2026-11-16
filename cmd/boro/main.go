package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/pelageech/boro"
	"github.com/pelageech/boro/command"
	"github.com/pelageech/boro/examples/counter"
	"github.com/pelageech/boro/examples/counter/schema"
	"github.com/pelageech/boro/scenario"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// The bundled runner demonstrates explicit wiring with the Counter capability.
func run(ctx context.Context) error {
	backend := counter.NewMemoryBackend()
	capability, err := counter.NewCapability(func(context.Context, boro.EnvironmentManifest) (counter.Backend, error) { return backend, nil })
	if err != nil {
		return err
	}
	composition, err := boro.Compose("example.counter.v1", capability)
	if err != nil {
		return err
	}
	return command.New(command.Options{Composition: composition, Profiles: []scenario.ProfileDeclaration{schema.Profile()}}).ExecuteContext(ctx)
}

package counter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"boro/examples/counter/schema"
	"boro/scenario"
)

// Spec is the same pure data type used by YAML resources.
type Spec = schema.Counter

type Client interface {
	Add(context.Context, int) error
	Value(context.Context) (int, error)
	Close(context.Context) error
}

// Backend opens independently owned handles to a named counter.
type Backend interface {
	Open(context.Context, Spec) (Client, error)
}

// Counter exposes domain behavior independently of scenario execution.
// A direct caller owns Close; managed execution transfers it to RunScope.
type Counter struct {
	client Client
}

// New validates the spec before opening a client. A partial acquisition is
// released locally because no Counter has been returned to the caller.
func New(ctx context.Context, spec Spec, backend Backend) (*Counter, error) {
	if err := schema.ValidateCounter(spec); err != nil {
		return nil, fmt.Errorf("validate counter: %w", err)
	}
	if backend == nil {
		return nil, fmt.Errorf("counter backend is required: %w", scenario.ErrInput)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open counter: %w", err)
	}
	client, err := backend.Open(ctx, spec)
	if err != nil {
		if client != nil {
			err = errors.Join(err, releaseUnowned(ctx, client))
		}
		return nil, fmt.Errorf("open counter: %w", err)
	}
	if client == nil {
		return nil, fmt.Errorf("counter backend returned no client: %w", scenario.ErrInput)
	}
	return &Counter{client: client}, nil
}

func (c *Counter) Add(ctx context.Context, by int) error {
	if err := schema.ValidateIncrement(schema.IncrementInput{By: by}); err != nil {
		return fmt.Errorf("validate increment: %w", err)
	}
	if err := c.client.Add(ctx, by); err != nil {
		return fmt.Errorf("add counter: %w", err)
	}
	return nil
}

func (c *Counter) Value(ctx context.Context) (int, error) {
	value, err := c.client.Value(ctx)
	if err != nil {
		return 0, fmt.Errorf("read counter: %w", err)
	}
	return value, nil
}

func (c *Counter) Close(ctx context.Context) error {
	if err := c.client.Close(ctx); err != nil {
		return fmt.Errorf("close counter: %w", err)
	}
	return nil
}

func releaseUnowned(ctx context.Context, client Client) error {
	// Failed acquisition/ownership must release even after caller cancellation.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := client.Close(cleanup); err != nil {
		return fmt.Errorf("release unowned counter client: %w", err)
	}
	return nil
}

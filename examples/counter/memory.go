package counter

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrClosed = errors.New("counter client is closed")

// MemoryBackend owns values by counter name. Separate backend instances are
// isolated; clients opened from one instance deliberately share its values.
type MemoryBackend struct {
	mu     sync.Mutex
	values map[string]int
}

func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{values: make(map[string]int)}
}

func (b *MemoryBackend) Open(ctx context.Context, spec Spec) (Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open memory counter: %w", err)
	}
	return &memoryClient{backend: b, name: spec.Name}, nil
}

type memoryClient struct {
	backend *MemoryBackend
	name    string
	closed  bool
}

func (c *memoryClient) Add(ctx context.Context, by int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("add memory counter: %w", err)
	}
	c.backend.mu.Lock()
	defer c.backend.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if c.backend.values == nil {
		c.backend.values = make(map[string]int)
	}
	c.backend.values[c.name] += by
	return nil
}

func (c *memoryClient) Value(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("read memory counter: %w", err)
	}
	c.backend.mu.Lock()
	defer c.backend.mu.Unlock()
	if c.closed {
		return 0, ErrClosed
	}
	return c.backend.values[c.name], nil
}

func (c *memoryClient) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("close memory counter: %w", err)
	}
	c.backend.mu.Lock()
	defer c.backend.mu.Unlock()
	c.closed = true
	return nil
}

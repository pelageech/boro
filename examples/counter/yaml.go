package counter

import (
	"context"
	"fmt"

	"boro/examples/counter/schema"
)

// ParseYAML validates a standalone counter spec without opening clients.
func ParseYAML(data []byte) (Spec, error) {
	spec, err := schema.ParseYAML(data)
	if err != nil {
		return Spec{}, fmt.Errorf("parse counter spec: %w", err)
	}
	return spec, nil
}

// FromYAML constructs the same Counter as New, after strict offline decoding.
func FromYAML(ctx context.Context, data []byte, backend Backend) (*Counter, error) {
	spec, err := ParseYAML(data)
	if err != nil {
		return nil, err
	}
	return New(ctx, spec, backend)
}

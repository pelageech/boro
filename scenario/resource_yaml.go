package scenario

import (
	"fmt"
	"reflect"
)

// ParseResourceYAML decodes a standalone resource spec using the same strict YAML
// rules and explicit validator as scenario loading. It performs no acquisition.
// Profile, resource-reference and environment binding checks still belong to
// loading and resolving the complete scenario.
func ParseResourceYAML[T any](data []byte, entry ResourceRegistration[T]) (T, error) {
	var zero T
	schema, err := WithResource(Schema{standalone: true}, entry)
	if err != nil {
		return zero, fmt.Errorf("declare YAML resource: %w", err)
	}
	document, source, err := parseYAML("<resource>", data)
	if err != nil {
		return zero, err
	}
	value, err := decodeNode[T](document.Content[0])
	if err != nil {
		return zero, source.decoding(err)
	}
	if err := schema.validateDataTree(reflect.ValueOf(value), "", reflect.StructField{}); err != nil {
		return zero, source.diagnostic(err)
	}
	if entry.Validate != nil {
		if err := entry.Validate(ValidationContext{}, Clone(value)); err != nil {
			return zero, source.diagnostic(err)
		}
	}
	return value, nil
}

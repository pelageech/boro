package scenario

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInput classifies rejected catalog, selection, and binding input.
var ErrInput = errors.New("invalid scenario input")

// InputError identifies the file, original source location, and typed field.
type InputError struct {
	File     string
	Line     int
	Column   int
	Field    string
	Category string
	Cause    error
}

func (e *InputError) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s %s: %v", e.File, e.Line, e.Column, e.Category, e.Field, e.Cause)
}

// Unwrap preserves the underlying parse or I/O error.
func (e *InputError) Unwrap() error { return e.Cause }

// Is classifies all input errors without losing their precise diagnostics.
func (e *InputError) Is(target error) bool { return target == ErrInput }

// ValidationError records a field-specific language or binding violation.
type ValidationError struct {
	Field   string
	Message string
	cause   error
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

func (e *ValidationError) Unwrap() error { return e.cause }

func invalid(field, message string) *ValidationError {
	return &ValidationError{Field: field, Message: message}
}

func PrefixIssue(path string, err error) *ValidationError {
	if detail, ok := errors.AsType[*ValidationError](err); ok {
		field := strings.Trim(path+"."+strings.Trim(detail.Field, "."), ".")
		return &ValidationError{Field: strings.ReplaceAll(field, ".[", "["), Message: detail.Message, cause: err}
	}
	return &ValidationError{Field: path, Message: err.Error(), cause: err}
}

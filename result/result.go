// Package result contains operation evidence and protocol error classifications.
package result

import (
	"errors"
	"fmt"
)

// Action records operation-owned evidence, including expected rejection.
type Action struct {
	Detail string `json:"detail,omitempty"`
}

// Observation separates a typed comparison from a transport or parsing error.
type Observation struct {
	Matches bool   `json:"matches"`
	Detail  string `json:"detail,omitempty"`
}

var ErrMismatch = errors.New("observation mismatch")

var ErrResetParse = errors.New("reset protocol parse failure")

func ResetParseError(err error) error {
	return fmt.Errorf("%w: %w", ErrResetParse, err)
}

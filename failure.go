package boro

import (
	"context"
	"errors"

	runresult "github.com/pelageech/boro/result"
)

// Failure classifies domain reset and resource-release diagnostics.
type Failure struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

func failure(category string, err error) *Failure {
	if err == nil {
		return nil
	}
	if category == "" {
		category = "operation"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			category = "deadline"
		case cancellationOnly(err):
			category = "cancellation"
		case errors.Is(err, runresult.ErrMismatch):
			category = "mismatch"
		}
	}
	return &Failure{Category: category, Message: err.Error()}
}

func cancellationOnly(err error) bool {
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		for _, child := range joined.Unwrap() {
			if !cancellationOnly(child) {
				return false
			}
		}
		return true
	}
	var wrapped interface{ Unwrap() error }
	if errors.As(err, &wrapped) {
		return cancellationOnly(wrapped.Unwrap())
	}
	return errors.Is(err, context.Canceled)
}

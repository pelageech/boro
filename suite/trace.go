package suite

import (
	"time"

	runresult "boro/result"
)

// ObservationSample is attached to the native spec for each actual protocol
// read. Its interval allows overlap assertions without a separate event log.
type ObservationSample struct {
	StepID      string                `json:"step_id"`
	StartedAt   time.Time             `json:"started_at"`
	FinishedAt  time.Time             `json:"finished_at"`
	Observation runresult.Observation `json:"observation"`
	Error       string                `json:"error,omitempty"`
}

// ActionCompletion records the actual bounded mutation, never a retry.
type ActionCompletion struct {
	StepID     string    `json:"step_id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Detail     string    `json:"detail,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// BranchCompletion records joining within one explicit YAML parallel block.
type BranchCompletion struct {
	StepID     string    `json:"step_id"`
	BranchID   string    `json:"branch_id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Error      string    `json:"error,omitempty"`
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

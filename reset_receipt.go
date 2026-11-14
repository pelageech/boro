package boro

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"time"
	"unicode/utf8"

	scen "boro/scenario"
)

// ResetContext is supplied by the owner before dispatch, independently of a receipt.
// Its targets come from the generation journal, never from observed receipt fields.
type ResetContext struct {
	Identity          ResultIdentity          `json:"identity"`
	Generation        GenerationIdentity      `json:"generation"`
	Baseline          BaselineIdentity        `json:"baseline"`
	ResetID           string                  `json:"reset_id" format:"id"`
	Boundary          uint64                  `json:"boundary" min:"1" max:"9007199254740991"`
	CaseFinishedAt    time.Time               `json:"case_finished_at"`
	CaseProcessExit   int                     `json:"case_process_exit"`
	CaseHelpersJoined bool                    `json:"case_helpers_joined"`
	BoundTargets      []scen.BoundResetTarget `json:"bound_targets"`
}

// ResetCompletion is independently observed by the owner after the reset process
// has terminated. Events are the archived acquisition/release log, not receipt data.
type ResetCompletion struct {
	ExitCode         int
	FinishedAt       time.Time
	Events           []ResetEvent
	BoundaryConsumed bool
}

// ResetEvent journals reset-local resources immediately at acquisition and release.
type ResetEvent struct {
	ResetID    string                `json:"reset_id" format:"id"`
	Boundary   uint64                `json:"boundary" min:"1" max:"9007199254740991"`
	Type       string                `json:"type" enum:"acquired|released|failed"`
	ResourceID string                `json:"resource_id" format:"id"`
	Kind       string                `json:"kind" enum:"client|helper"`
	Target     scen.BoundResetTarget `json:"target"`
	Locator    ResetLocator          `json:"locator"`
	At         time.Time             `json:"at"`
}

type resetObservationWire struct {
	CheckID     string                `json:"check_id" format:"id"`
	Domain      string                `json:"domain"`
	Target      scen.BoundResetTarget `json:"target"`
	Outcome     string                `json:"outcome" enum:"matched|mismatch|error|not_run"`
	StartedAt   *time.Time            `json:"started_at,omitempty"`
	CompletedAt *time.Time            `json:"completed_at,omitempty"`
	Actual      json.RawMessage       `json:"actual,omitempty"`
	ErrorIDs    []string              `json:"error_ids" format:"id"`
}

// ReadResetContext reads the independently fixed public-command dispatch inputs.
func ReadResetContext(reader io.Reader) (ResetContext, error) {
	data, err := io.ReadAll(io.LimitReader(reader, scen.DocumentLimit+1))
	if err != nil {
		return ResetContext{}, fmt.Errorf("read reset context: %w", err)
	}
	return scen.StrictResetJSON[ResetContext](data)
}

// ReadResetReceipt uses the expected check ID to select each actual's concrete type.
func ReadResetReceipt(reader io.Reader, baseline scen.ResetBaseline, schema scen.Schema) (ResetReceipt, error) {
	if err := schema.ValidateResetBaseline(baseline); err != nil {
		return ResetReceipt{}, fmt.Errorf("validate receipt baseline: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(reader, scen.DocumentLimit+1))
	if err != nil {
		return ResetReceipt{}, fmt.Errorf("read reset receipt: %w", err)
	}
	if len(data) > scen.DocumentLimit || !utf8.Valid(data) {
		return ResetReceipt{}, inputError("receipt", "oversized or invalid UTF-8 receipt")
	}
	raw, err := scen.ReadJSON[map[string]json.RawMessage](bytes.NewReader(data))
	if err != nil {
		return ResetReceipt{}, err
	}
	observations, exists := raw["observations"]
	if !exists {
		return ResetReceipt{}, inputError("observations", "required field is missing")
	}
	// Decode the header with an empty array; actuals require the external baseline.
	raw["observations"] = json.RawMessage("[]")
	header, err := json.Marshal(raw)
	if err != nil {
		return ResetReceipt{}, fmt.Errorf("encode reset header: %w", err)
	}
	receipt, err := scen.StrictResetJSON[ResetReceipt](header)
	if err != nil {
		return ResetReceipt{}, err
	}
	wire, err := scen.StrictResetJSON[[]resetObservationWire](observations)
	if err != nil {
		return ResetReceipt{}, err
	}
	checks := make(map[string]scen.ExpectedCheck)
	for _, check := range baseline.ExpectedChecks {
		checks[check.ID] = check
	}
	for _, row := range wire {
		check, exists := checks[row.CheckID]
		if !exists {
			return ResetReceipt{}, inputError("observations", "extra or duplicate check "+row.CheckID)
		}
		delete(checks, row.CheckID)
		observation := ResetObservation{
			CheckID: row.CheckID, Domain: row.Domain, Target: row.Target, Outcome: row.Outcome,
			StartedAt: row.StartedAt, CompletedAt: row.CompletedAt, ErrorIDs: row.ErrorIDs,
		}
		if row.Actual != nil {
			_, _, actualType, err := schema.ResetCheckTypes(check.Kind)
			if err != nil {
				return ResetReceipt{}, fmt.Errorf("select reset observation type: %w", err)
			}
			observation.Actual, err = schema.DecodeResetValue(row.Actual, actualType)
			if err != nil {
				return ResetReceipt{}, fmt.Errorf("decode reset observation: %w", err)
			}
		}
		receipt.Observations = append(receipt.Observations, observation)
	}
	if len(checks) != 0 || len(receipt.Observations) == 0 {
		return ResetReceipt{}, inputError("observations", "incomplete or empty reset observations")
	}
	if err := validateResetReceipt(baseline, receipt, schema); err != nil {
		return ResetReceipt{}, err
	}
	return receipt, nil
}

// ReadResetEvents reads every complete JSONL event and rejects trailing fragments.
func ReadResetEvents(reader io.Reader) ([]ResetEvent, error) {
	data, err := io.ReadAll(io.LimitReader(reader, scen.DocumentLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read reset events: %w", err)
	}
	if len(data) > scen.DocumentLimit || (len(data) != 0 && data[len(data)-1] != '\n') {
		return nil, inputError("events", "oversized or truncated event log")
	}
	events := []ResetEvent{}
	for line := range bytes.SplitSeq(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		event, err := scen.StrictResetJSON[ResetEvent](line)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func validateResetReceipt(baseline scen.ResetBaseline, receipt ResetReceipt, schema scen.Schema) error {
	if err := validateResetCaseIdentity(receipt.Identity); err != nil {
		return err
	}
	if receipt.Version != 1 || receipt.CompletedAt.Before(receipt.StartedAt) ||
		receipt.StartedAt.Before(receipt.CaseFinishedAt) || receipt.Generation.Project != receipt.Identity.Project {
		return inputError("receipt", "invalid version, generation or reset interval")
	}
	checks := make(map[string]scen.ExpectedCheck)
	for _, check := range baseline.ExpectedChecks {
		checks[check.ID] = check
	}
	if len(checks) == 0 || len(checks) != len(receipt.Observations) {
		return inputError("observations", "nonempty complete expected set required")
	}
	errorsByID, err := resetErrorIndex(receipt.Errors)
	if err != nil {
		return err
	}
	referenced := make(map[string]bool)
	for _, row := range receipt.Observations {
		check, exists := checks[row.CheckID]
		if !exists || row.Domain != check.Domain || row.Target.Service != check.Target.Service {
			return inputError("observations", "extra, duplicate or wrong domain/target")
		}
		delete(checks, row.CheckID)
		if err := validateResetObservation(check, row, receipt, schema); err != nil {
			return err
		}
		if err := resetErrorReferences(row.ErrorIDs, row.CheckID, "", errorsByID, referenced); err != nil {
			return err
		}
	}
	seen := make(map[string]bool)
	for _, resource := range receipt.Resources {
		if seen[resource.ID] || resource.OwnerResetID != receipt.ResetID {
			return inputError("resources", "duplicate resource or wrong reset owner")
		}
		seen[resource.ID] = true
		if err := validateResetResource(resource, receipt); err != nil {
			return err
		}
		if err := resetErrorReferences(resource.ErrorIDs, "", resource.ID, errorsByID, referenced); err != nil {
			return err
		}
	}
	for _, failure := range receipt.Errors {
		if (failure.CheckID != "" || failure.ResourceID != "") && !referenced[failure.ID] {
			return inputError("errors", "located error is not referenced by its row")
		}
	}
	return nil
}

func resetErrorIndex(failures []ResetError) (map[string]ResetError, error) {
	index := make(map[string]ResetError)
	for _, failure := range failures {
		if _, duplicate := index[failure.ID]; duplicate || (failure.CheckID != "" && failure.ResourceID != "") {
			return nil, inputError("errors", "duplicate error or conflicting location")
		}
		index[failure.ID] = failure
	}
	return index, nil
}

func validateResetCaseIdentity(identity ResultIdentity) error {
	if err := scen.ResetString(identity.ScenarioID, reflect.StructField{Tag: `format:"id"`}); err != nil {
		return fmt.Errorf("validate reset value: %w", err)
	}
	for _, hash := range []string{identity.ScenarioHash, identity.ResolvedHash} {
		if err := scen.ResetString(hash, reflect.StructField{Tag: `format:"sha256"`}); err != nil {
			return fmt.Errorf("validate reset value: %w", err)
		}
	}
	return nil
}

func validateResetObservation(check scen.ExpectedCheck, row ResetObservation, receipt ResetReceipt, schema scen.Schema) error {
	switch row.Outcome {
	case scen.ResetMatched, scen.ResetMismatch:
		if row.Actual == nil || len(row.ErrorIDs) != 0 {
			return inputError("observation", "comparison requires actual and forbids errors")
		}
		if err := schema.ValidateObservedResetValue(check, row.Actual); err != nil {
			return fmt.Errorf("validate reset inventory: %w", err)
		}
		matches, err := schema.CompareResetValue(check, row.Actual)
		if err != nil {
			return fmt.Errorf("compare reset inventory: %w", err)
		}
		if (row.Outcome == scen.ResetMatched) != matches {
			return inputError("observation", "declared outcome contradicts typed comparison")
		}
	case scen.ResetOutcomeError, scen.ResetNotRun:
		if row.Actual != nil || len(row.ErrorIDs) == 0 {
			return inputError("observation", "failed observation forbids actual and requires errors")
		}
	default:
		return inputError("outcome", "unknown reset outcome")
	}
	if row.Outcome == scen.ResetNotRun {
		if row.StartedAt != nil || row.CompletedAt != nil {
			return inputError("observation", "not_run forbids timestamps")
		}
		return nil
	}
	if row.StartedAt == nil || row.CompletedAt == nil || row.StartedAt.Before(receipt.StartedAt) ||
		row.CompletedAt.Before(*row.StartedAt) || row.CompletedAt.After(receipt.CompletedAt) {
		return inputError("observation", "observation interval must lie inside reset")
	}
	return nil
}

func validateResetResource(resource ResetResource, receipt ResetReceipt) error {
	if resource.AcquiredAt.Before(receipt.StartedAt) || resource.AcquiredAt.After(receipt.CompletedAt) {
		return inputError("resource", "acquisition lies outside reset")
	}
	if err := validateResetLocator(resource); err != nil {
		return err
	}
	switch resource.State {
	case ResourceReleased:
		if !resource.Joined || resource.ReleasedAt == nil || resource.ReleasedAt.Before(resource.AcquiredAt) ||
			resource.ReleasedAt.After(receipt.CompletedAt) || len(resource.ErrorIDs) != 0 {
			return inputError("resource", "released resource requires joined work, valid release time and no errors")
		}
	case resourceFailed:
		if resource.ReleasedAt != nil || len(resource.ErrorIDs) == 0 {
			return inputError("resource", "failed resource requires cleanup errors and forbids release time")
		}
	default:
		return inputError("resource", "unknown resource state")
	}
	return nil
}

func validateResetLocator(resource ResetResource) error {
	locator := resource.Locator
	switch locator.Type {
	case string(ResourceClient):
		if resource.Kind != string(ResourceClient) || locator.ContainerID != "" || locator.PID != nil || locator.ProcessStartedAt != nil {
			return inputError("locator", "client contains helper fields or wrong kind")
		}
		return validateResetClientLocator(locator)
	case scen.ResetContainer, scen.ResetProcess:
		if resource.Kind != string(ResourceHelper) || locator.ContainerID == "" || locator.Protocol != "" || locator.Endpoint != "" ||
			(locator.Type == scen.ResetProcess) != (locator.PID != nil && locator.ProcessStartedAt != nil) {
			return inputError("locator", "invalid helper identity")
		}
		if locator.Type == scen.ResetContainer && (locator.PID != nil || locator.ProcessStartedAt != nil) {
			return inputError("locator", "container helper forbids process fields")
		}
		if locator.Type == scen.ResetProcess && locator.ProcessStartedAt.After(resource.AcquiredAt) {
			return inputError("locator", "helper process starts after its acquisition")
		}
	default:
		return inputError("locator", "unknown locator type")
	}
	return nil
}

func validateResetClientLocator(locator ResetLocator) error {
	switch locator.Protocol {
	case scen.ResetGRPC:
		host, port, err := net.SplitHostPort(locator.Endpoint)
		number, portErr := strconv.ParseUint(port, 10, 16)
		if err != nil || portErr != nil || host == "" || number == 0 {
			return inputError("locator", "gRPC endpoint must be host:port")
		}
	case scen.ResetHTTP:
		target, err := url.Parse(locator.Endpoint)
		if err != nil || target.Host == "" || target.User != nil ||
			(target.Scheme != scen.ResetHTTP && target.Scheme != scen.ResetHTTPS) {
			return inputError("locator", "HTTP endpoint must be an absolute URL")
		}
	case scen.ResetUnix:
		if !filepath.IsAbs(locator.Endpoint) {
			return inputError("locator", "Unix endpoint must be absolute")
		}
	default:
		return inputError("locator", "unknown client protocol")
	}
	return nil
}

func resetErrorReferences(ids []string, checkID, resourceID string, failures map[string]ResetError, referenced map[string]bool) error {
	if scen.DuplicateResetStrings(ids) {
		return inputError("error_ids", "duplicate error reference")
	}
	for _, id := range ids {
		failure, exists := failures[id]
		if !exists || (failure.CheckID != "" && failure.CheckID != checkID) ||
			(failure.ResourceID != "" && failure.ResourceID != resourceID) {
			return inputError("error_ids", "unknown or incorrectly located error")
		}
		if resourceID != "" && failure.Category != "cleanup" {
			return inputError("error_ids", "resource errors must describe cleanup")
		}
		referenced[id] = true
	}
	return nil
}

type GenerationIdentity struct {
	BatchID           string `json:"batch_id" format:"id"`
	Worker            uint64 `json:"worker" max:"9007199254740991"`
	Generation        uint64 `json:"generation" min:"1" max:"9007199254740991"`
	Project           string `json:"project"`
	CompatibilityHash string `json:"compatibility_hash" format:"sha256"`
}

type BaselineIdentity struct {
	ID     string `json:"id" format:"id"`
	SHA256 string `json:"sha256" format:"sha256"`
}

type ResetReceipt struct {
	Version        int                `json:"version"`
	Identity       ResultIdentity     `json:"identity"`
	Generation     GenerationIdentity `json:"generation"`
	Baseline       BaselineIdentity   `json:"baseline"`
	ResetID        string             `json:"reset_id" format:"id"`
	Boundary       uint64             `json:"boundary" min:"1" max:"9007199254740991"`
	CaseFinishedAt time.Time          `json:"case_finished_at"`
	StartedAt      time.Time          `json:"started_at"`
	CompletedAt    time.Time          `json:"completed_at"`
	Observations   []ResetObservation `json:"observations"`
	Resources      []ResetResource    `json:"resources"`
	Errors         []ResetError       `json:"errors"`
}

type ResetObservation struct {
	CheckID     string                `json:"check_id" format:"id"`
	Domain      string                `json:"domain"`
	Target      scen.BoundResetTarget `json:"target"`
	Outcome     string                `json:"outcome" enum:"matched|mismatch|error|not_run"`
	StartedAt   *time.Time            `json:"started_at,omitempty"`
	CompletedAt *time.Time            `json:"completed_at,omitempty"`
	Actual      scen.ResetValue       `json:"actual,omitempty"`
	ErrorIDs    []string              `json:"error_ids" format:"id"`
}

type ResetResource struct {
	ID           string                `json:"id" format:"id"`
	Kind         string                `json:"kind" enum:"client|helper"`
	OwnerResetID string                `json:"owner_reset_id" format:"id"`
	Target       scen.BoundResetTarget `json:"target"`
	Locator      ResetLocator          `json:"locator"`
	AcquiredAt   time.Time             `json:"acquired_at"`
	Joined       bool                  `json:"joined"`
	State        string                `json:"state" enum:"released|failed"`
	ReleasedAt   *time.Time            `json:"released_at,omitempty"`
	ErrorIDs     []string              `json:"error_ids" format:"id"`
}

type ResetLocator struct {
	Type             string     `json:"type" enum:"client|container|process"`
	Protocol         string     `json:"protocol,omitempty" enum:"grpc|http|unix"`
	Endpoint         string     `json:"endpoint,omitempty"`
	ContainerID      string     `json:"container_id,omitempty" format:"sha256"`
	PID              *uint32    `json:"pid,omitempty" min:"1"`
	ProcessStartedAt *time.Time `json:"process_started_at,omitempty"`
}

type ResetError struct {
	ID         string `json:"id" format:"id"`
	Category   string `json:"category" enum:"transport|parse|operation|mismatch|deadline|cancellation|identity|validation|cleanup"`
	Message    string `json:"message"`
	CheckID    string `json:"check_id,omitempty" format:"id"`
	ResourceID string `json:"resource_id,omitempty" format:"id"`
}

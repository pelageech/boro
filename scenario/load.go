package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LoadScenario parses and validates one file without invoking any lab services.
func LoadScenario(ctx context.Context, path string, options LoadOptions) (Scenario, error) {
	if err := ctx.Err(); err != nil {
		return Scenario{}, fmt.Errorf("load canceled: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("scenario path: %w", err)
	}
	data, err := os.ReadFile(absolute) //nolint:gosec // G304: loading the caller-selected scenario path is this API's contract.
	if err != nil {
		return Scenario{}, &InputError{File: absolute, Line: 1, Column: 1, Category: "decoding", Cause: err}
	}
	wire, source, err := parseDocument(absolute, data)
	if err != nil {
		return Scenario{}, err
	}
	definition, err := decodeDefinition(wire, options.Schema)
	if err != nil {
		return Scenario{}, source.decoding(err)
	}
	profile, err := chooseProfile(definition.Environment.Profile, options)
	if err != nil {
		return Scenario{}, source.diagnostic(err)
	}
	profile = Clone(profile)
	limit := options.ParallelActions
	if limit == 0 {
		limit = 2
	}
	if limit < 2 {
		return Scenario{}, source.diagnostic(invalid("parallel-actions", "branch limit must be at least two"))
	}
	if err := options.Schema.ValidateDefinition(definition, profile, limit); err != nil {
		return Scenario{}, source.diagnostic(err)
	}
	fixtureRoot := options.FixtureRoot
	if fixtureRoot == "" {
		fixtureRoot = filepath.Join(filepath.Dir(filepath.Dir(absolute)), "fixtures")
	}
	assets, err := options.Schema.captureAssets(ctx, definition, fixtureRoot)
	if err != nil {
		return Scenario{}, source.diagnostic(err)
	}
	sourceHash := Digest(data)
	hashInput, err := json.Marshal(struct {
		Source string
		Assets []Asset
	}{Source: sourceHash, Assets: assets})
	if err != nil {
		return Scenario{}, fmt.Errorf("hash scenario: %w", err)
	}
	return Scenario{
		definition: definition, source: absolute, sourceHash: sourceHash, hash: Digest(hashInput), assets: assets,
		profile: profile, branchLimit: limit, schema: options.Schema,
	}, nil
}

func Digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

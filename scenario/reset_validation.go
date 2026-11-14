package scenario

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

func (s Schema) ValidateObservedResetValue(check ExpectedCheck, actual any) error {
	_, _, shape, err := s.ResetCheckTypes(check.Kind)
	if err != nil {
		return err
	}
	if reflect.TypeOf(actual) != shape {
		return invalid("actual", "observer returned the wrong concrete type")
	}
	data, err := json.Marshal(actual)
	if err != nil {
		return fmt.Errorf("encode reset observation: %w", err)
	}
	if _, err := s.DecodeResetValue(data, shape); err != nil {
		return err
	}
	return s.resetCheckEntries()[check.Kind].ValidateActual(Clone(actual))
}

func (s Schema) ValidateResetBaseline(b ResetBaseline) error {
	if b.Version != 1 || len(b.Domains) == 0 || len(b.ExpectedChecks) == 0 {
		return invalid("baseline", "version 1, domains and a nonempty complete check set are required")
	}
	assets, err := resetAssetIndex(b.Assets)
	if err != nil {
		return err
	}
	// Apply the same strict envelope and payload constraints to native Go data.
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("encode reset baseline: %w", err)
	}
	if _, err := StrictResetJSON[resetBaselineWire](data); err != nil {
		return err
	}
	domains, err := s.resetDomainIndex(b.Domains, assets)
	if err != nil {
		return err
	}
	if err := s.resetProfileDomains(b.Profile, domains); err != nil {
		return err
	}
	seen, scopes, covered := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	for _, check := range b.ExpectedChecks {
		if _, exists := domains[check.Domain]; !exists || seen[check.ID] {
			return invalid("expected_checks", "duplicate check ID or undeclared domain")
		}
		ctx := ResetValidationContext{Profile: b.Profile, Assets: b.Assets, Target: check.Target}
		if err := s.validateExpectedCheck(check, ctx); err != nil {
			return PrefixIssue(check.ID, err)
		}
		scope, err := json.Marshal(check.Scope)
		if err != nil {
			return fmt.Errorf("encode reset scope: %w", err)
		}
		key := check.Target.Service + "/" + check.Kind + "/" + string(scope)
		if scopes[key] {
			return invalid("expected_checks", "duplicate target/kind/scope")
		}
		seen[check.ID], scopes[key], covered[check.Domain] = true, true, true
	}
	if len(covered) != len(domains) {
		return invalid("expected_checks", "every domain requires checks")
	}
	for _, domain := range b.Domains {
		if err := s.resetDomainEntries()[domain.Name].Validate(Clone(b), Clone(domain)); err != nil {
			return PrefixIssue("domains."+domain.Name, err)
		}
	}
	return nil
}

func (s Schema) validateExpectedCheck(check ExpectedCheck, ctx ResetValidationContext) error {
	descriptor, exists := s.resetCheckEntries()[check.Kind]
	if !exists {
		return invalid("kind", "unknown reset check "+check.Kind)
	}
	if !slices.Contains(descriptor.Domains, check.Domain) {
		return invalid("domain", "check does not belong to this reset domain")
	}
	if reflect.TypeOf(check.Scope) != descriptor.Scope || reflect.TypeOf(check.Expected) != descriptor.Expected {
		return invalid("expected_checks", "wrong concrete scope or expected type")
	}
	for _, value := range []any{check.Scope, check.Expected} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode reset payload: %w", err)
		}
		if _, err := s.DecodeResetValue(encoded, reflect.TypeOf(value)); err != nil {
			return err
		}
	}
	return descriptor.Validate(Clone(ctx), Clone(check.Scope), Clone(check.Expected))
}

func resetAssetIndex(values []ResetAsset) (map[string]ResetAsset, error) {
	assets := make(map[string]ResetAsset)
	assetTargets := make(map[string]bool)
	for _, asset := range values {
		key := asset.Service + "/" + asset.Purpose + "/" + asset.TargetPath
		if _, duplicate := assets[asset.ID]; duplicate || assetTargets[key] {
			return nil, invalid("assets", "duplicate asset ID or service/purpose/path")
		}
		if !filepath.IsLocal(asset.Path) || strings.Contains(asset.Path, "\\") ||
			slices.Contains(strings.Split(asset.Path, "/"), "..") || !filepath.IsAbs(asset.TargetPath) {
			return nil, invalid("assets", "asset path must be contained and target path absolute")
		}
		assets[asset.ID], assetTargets[key] = asset, true
	}
	return assets, nil
}

func (s Schema) resetDomainIndex(values []ResetDomain, assets map[string]ResetAsset) (map[string]ResetDomain, error) {
	domains := make(map[string]ResetDomain)
	references := make(map[string]bool)
	for _, domain := range values {
		if _, exists := s.resetDomainEntries()[domain.Name]; !exists {
			return nil, invalid("domains", "unknown reset domain "+domain.Name)
		}
		if _, duplicate := domains[domain.Name]; duplicate {
			return nil, invalid("domains", "duplicate reset domain")
		}
		domains[domain.Name] = domain
		for _, id := range domain.AssetIDs {
			if _, exists := assets[id]; !exists || references[id] {
				return nil, invalid("asset_ids", "missing or multiply referenced asset")
			}
			references[id] = true
		}
	}
	if len(references) != len(assets) {
		return nil, invalid("assets", "every asset must be referenced exactly once")
	}
	return domains, nil
}

func (s Schema) resetProfileDomains(profile string, domains map[string]ResetDomain) error {
	policy, exists := s.resetProfileEntries()[profile]
	if !exists {
		return invalid("profile", "unknown reset profile "+profile)
	}
	for _, required := range policy.RequiredDomains {
		if _, exists := domains[required]; !exists {
			return invalid("domains", "profile baseline obligations are missing or incompatible")
		}
	}
	for domain := range domains {
		if !slices.Contains(policy.AllowedDomains, domain) {
			return invalid("domains", "profile baseline obligations are missing or incompatible")
		}
	}
	return nil
}

func DuplicateResetStrings(values []string) bool {
	seen := make(map[string]bool)
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func (b ResetBaseline) ValidateCaptured() error {
	fields, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}
	if b.sha256 == "" || Digest(fields) != b.fieldsSHA256 {
		return invalid("baseline", "baseline must retain its exact immutable captured fields and byte hash")
	}
	return b.schema.ValidateResetBaseline(b)
}

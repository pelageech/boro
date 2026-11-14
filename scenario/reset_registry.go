package scenario

import (
	"maps"
	"reflect"
	"slices"
)

type ResetValidationContext struct {
	Profile string
	Assets  []ResetAsset
	Target  ResetTarget
}

type ResetCheckRegistration[S, E, A any] struct {
	Kind           string
	Domains        []string
	Validate       func(ResetValidationContext, S, E) error
	ValidateActual func(A) error
	Compare        func(E, A) (bool, error)
}

type ResetDomainRegistration struct {
	Name     string
	Validate func(ResetBaseline, ResetDomain) error
}

type ResetProfilePolicy struct {
	Profile         string
	RequiredDomains []string
	AllowedDomains  []string
}

// ResetCheckDescriptor retains concrete wire identities and detached callbacks.
type ResetCheckDescriptor struct {
	Kind                    string
	Domains                 []string
	Scope, Expected, Actual reflect.Type
	Validate                func(ResetValidationContext, any, any) error
	ValidateActual          func(any) error
	Compare                 func(any, any) (bool, error)
}

func WithResetCheck[S, E, A any](s Schema, entry ResetCheckRegistration[S, E, A]) (Schema, error) {
	if !ValidName(entry.Kind) || len(entry.Domains) == 0 || entry.Validate == nil || entry.ValidateActual == nil || entry.Compare == nil {
		return Schema{}, invalid("registry", "reset kind, domains and complete callbacks are required")
	}
	if _, exists := s.resetCheckEntries()[entry.Kind]; exists {
		return Schema{}, invalid("registry", "reset kind already registered: "+entry.Kind)
	}
	if DuplicateResetStrings(entry.Domains) {
		return Schema{}, invalid("registry", "duplicate reset check domain")
	}
	for _, domain := range entry.Domains {
		if _, exists := s.resetDomainEntries()[domain]; !exists {
			return Schema{}, invalid("registry", "undeclared reset domain "+domain)
		}
	}
	d := ResetCheckDescriptor{
		Kind: entry.Kind, Domains: slices.Clone(entry.Domains),
		Scope: reflect.TypeFor[S](), Expected: reflect.TypeFor[E](), Actual: reflect.TypeFor[A](),
	}
	for _, shape := range []reflect.Type{d.Scope, d.Expected, d.Actual} {
		if shape.Kind() != reflect.Struct {
			return Schema{}, invalid("registry", "reset payloads require concrete structs")
		}
		if err := s.validateResetShape(shape, make(map[reflect.Type]bool)); err != nil {
			return Schema{}, PrefixIssue("registry."+entry.Kind, err)
		}
	}
	d.Validate = func(ctx ResetValidationContext, scope, expected any) error {
		typedScope, scopeOK := scope.(S)
		typedExpected, expectedOK := expected.(E)
		if !scopeOK || !expectedOK {
			return invalid("expected_checks", "wrong concrete scope or expected type")
		}
		return entry.Validate(Clone(ctx), Clone(typedScope), Clone(typedExpected))
	}
	d.ValidateActual = func(actual any) error {
		typed, ok := actual.(A)
		if !ok {
			return invalid("actual", "observer returned the wrong concrete type")
		}
		return entry.ValidateActual(Clone(typed))
	}
	d.Compare = func(expected, actual any) (bool, error) {
		want, expectedOK := expected.(E)
		got, actualOK := actual.(A)
		if !expectedOK || !actualOK {
			return false, invalid("actual", "wrong concrete expected or actual type")
		}
		return entry.Compare(Clone(want), Clone(got))
	}
	checks := maps.Clone(s.resetCheckEntries())
	checks[d.Kind] = d
	s.resetChecks = checks
	return s, nil
}

func (s Schema) WithResetDomain(entry ResetDomainRegistration) (Schema, error) {
	if !ValidName(entry.Name) || entry.Validate == nil {
		return Schema{}, invalid("registry", "reset domain name and validator are required")
	}
	domains := maps.Clone(s.resetDomainEntries())
	if _, exists := domains[entry.Name]; exists {
		return Schema{}, invalid("registry", "reset domain already registered: "+entry.Name)
	}
	validate := entry.Validate
	entry.Validate = func(baseline ResetBaseline, domain ResetDomain) error {
		return validate(Clone(baseline), Clone(domain))
	}
	domains[entry.Name] = entry
	s.resetDomains = domains
	return s, nil
}

func (s Schema) WithResetProfile(policy ResetProfilePolicy) (Schema, error) {
	if !ValidName(policy.Profile) || len(policy.AllowedDomains) == 0 ||
		DuplicateResetStrings(policy.RequiredDomains) || DuplicateResetStrings(policy.AllowedDomains) {
		return Schema{}, invalid("registry", "reset profile requires unique declared domain policies")
	}
	profiles := maps.Clone(s.resetProfileEntries())
	if _, exists := profiles[policy.Profile]; exists {
		return Schema{}, invalid("registry", "reset profile already registered: "+policy.Profile)
	}
	for _, domain := range policy.AllowedDomains {
		if _, exists := s.resetDomainEntries()[domain]; !exists {
			return Schema{}, invalid("registry", "undeclared reset policy domain "+domain)
		}
	}
	for _, domain := range policy.RequiredDomains {
		if !slices.Contains(policy.AllowedDomains, domain) {
			return Schema{}, invalid("registry", "required reset domain is not allowed")
		}
	}
	profiles[policy.Profile] = Clone(policy)
	s.resetProfiles = profiles
	return s, nil
}

func (s Schema) resetCheckEntries() map[string]ResetCheckDescriptor {
	if s.resetChecks == nil {
		return map[string]ResetCheckDescriptor{}
	}
	return s.resetChecks
}

func (s Schema) resetDomainEntries() map[string]ResetDomainRegistration {
	if s.resetDomains == nil {
		return map[string]ResetDomainRegistration{}
	}
	return s.resetDomains
}

func (s Schema) resetProfileEntries() map[string]ResetProfilePolicy {
	if s.resetProfiles == nil {
		return map[string]ResetProfilePolicy{}
	}
	return s.resetProfiles
}

func (s Schema) ResetCheckTypes(kind string) (reflect.Type, reflect.Type, reflect.Type, error) {
	d, exists := s.resetCheckEntries()[kind]
	if !exists {
		return nil, nil, nil, invalid("kind", "unknown reset check "+kind)
	}
	return d.Scope, d.Expected, d.Actual, nil
}

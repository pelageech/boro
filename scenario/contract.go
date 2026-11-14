package scenario

import (
	"maps"
	"reflect"
	"slices"
	"strings"
)

// FormatNames returns the named string constraints in stable order.
func (s Schema) FormatNames() []string { return slices.Sorted(maps.Keys(s.formatEntries())) }

// ValidatedTypes returns explicitly validated nested types in stable order.
func (s Schema) ValidatedTypes() []reflect.Type {
	types := slices.Collect(maps.Keys(s.validators()))
	slices.SortFunc(types, func(a, b reflect.Type) int {
		return strings.Compare(a.PkgPath()+"."+a.String(), b.PkgPath()+"."+b.String())
	})
	return types
}

// ResetCheckDescriptors returns detached concrete reset contracts.
func (s Schema) ResetCheckDescriptors() []ResetCheckDescriptor {
	entries := s.resetCheckEntries()
	result := make([]ResetCheckDescriptor, 0, len(entries))
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		d := entries[name]
		d.Domains = slices.Clone(d.Domains)
		result = append(result, d)
	}
	return result
}

func (s Schema) ResetDomainNames() []string {
	return slices.Sorted(maps.Keys(s.resetDomainEntries()))
}

func (s Schema) ResetProfilePolicies() []ResetProfilePolicy {
	entries := s.resetProfileEntries()
	result := make([]ResetProfilePolicy, 0, len(entries))
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		result = append(result, Clone(entries[name]))
	}
	return result
}

// Descriptors returns the complete schema in name order, without mutable maps.
func (s Schema) Descriptors() []Descriptor {
	entries := s.entries()
	result := make([]Descriptor, 0, len(entries))
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		result = append(result, entries[name])
	}
	return result
}

// ResourceDescriptors returns every resource decoding contract in name order.
func (s Schema) ResourceDescriptors() []ResourceDescriptor {
	entries := s.resourceEntries()
	result := make([]ResourceDescriptor, 0, len(entries))
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		result = append(result, entries[name])
	}
	return result
}

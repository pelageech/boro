package scenario

import (
	"net/netip"
	"slices"
	"sort"
	"strings"
)

// ServiceDeclaration supplies logical names and static protocol capabilities.
type ServiceDeclaration struct {
	Name         string   `json:"name"`
	Roles        []string `json:"roles"`
	Capabilities []string `json:"capabilities"`
	Families     []string `json:"families"`
	Activation   string   `json:"activation,omitempty"`
}

// ProfileDeclaration records types before environment allocation.
type ProfileDeclaration struct {
	Name         string               `json:"name"`
	Services     []ServiceDeclaration `json:"services"`
	Addresses    map[string]int       `json:"addresses"`
	Capabilities []string             `json:"capabilities"`
	Interfaces   []string             `json:"interfaces"`
}

func (p ProfileDeclaration) SelectServices(s Selector, capability string) ([]ServiceDeclaration, error) {
	var matches []ServiceDeclaration
	for _, service := range p.Services {
		match := service.Name == s.Service && s.Service != ""
		if s.Role != "" {
			match = slices.Contains(service.Roles, s.Role)
		}
		if match {
			if capability != "" && !slices.Contains(service.Capabilities, capability) {
				return nil, invalid("", "selected service lacks capability "+capability)
			}
			matches = append(matches, service)
		}
	}
	if len(matches) == 0 {
		return nil, invalid("", "selector resolves to no services")
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	if s.Match == MatchFirst {
		matches = matches[:1]
	}
	return matches, nil
}

func (p ProfileDeclaration) AddressFamily(a Address) (int, error) {
	if (a.Env == "") == (a.Literal == "") {
		return 0, invalid("", "address requires exactly one literal or environment reference")
	}
	if a.Env != "" {
		if !strings.HasPrefix(a.Env, "addresses.") || !ValidName(strings.TrimPrefix(a.Env, "addresses.")) {
			return 0, invalid("", "address reference must name addresses.name")
		}
		family, exists := p.Addresses[strings.TrimPrefix(a.Env, "addresses.")]
		if !exists || (family != 4 && family != 6) {
			return 0, invalid("", "unknown or untyped environment address "+a.Env)
		}
		return family, nil
	}
	ip, err := netip.ParseAddr(a.Literal)
	if err != nil || ip.Zone() != "" || ip.Is4In6() {
		return 0, invalid("", "invalid address")
	}
	if ip.Is4() {
		return 4, nil
	}
	return 6, nil
}

func chooseProfile(name string, options LoadOptions) (ProfileDeclaration, error) {
	profiles := options.Profiles
	for _, profile := range profiles {
		if profile.Name == name {
			return profile, nil
		}
	}
	return ProfileDeclaration{}, invalid("environment.profile", "unknown profile "+name)
}

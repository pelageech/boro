package scenario

import (
	"net/netip"
	"strings"
)

// Validate checks address syntax without requiring an environment allocation.
func (a Address) Validate() error {
	if (a.Env == "") == (a.Literal == "") {
		return invalid("", "address requires exactly one literal or environment reference")
	}
	if a.Env != "" {
		if !strings.HasPrefix(a.Env, "addresses.") || !ValidName(strings.TrimPrefix(a.Env, "addresses.")) {
			return invalid("", "address reference must name addresses.name")
		}
		return nil
	}
	ip, err := netip.ParseAddr(a.Literal)
	if err != nil || ip.Zone() != "" || ip.Is4In6() {
		return invalid("", "invalid address")
	}
	return nil
}

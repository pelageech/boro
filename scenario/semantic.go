package scenario

import (
	"net"
	"net/netip"
)

func ValidName(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func (s Selector) Validate() error {
	switch {
	case s.Service != "" && s.Role == "" && s.Match == "":
		return nil
	case s.Service == "" && s.Role != "" && (s.Match == MatchAll || s.Match == MatchFirst):
		return nil
	default:
		return invalid("", "selector requires service or role with match all|first")
	}
}

func (w Wait) Validate() error {
	switch w.Mode {
	case "once":
		if w.Timeout != nil || w.Duration != nil || w.Poll != nil {
			return invalid("", "once forbids timeout, duration, and poll")
		}
	case "eventually":
		if w.Timeout == nil || w.Duration != nil {
			return invalid("", "eventually requires timeout and forbids duration")
		}
	case "remains":
		if w.Duration == nil || w.Timeout != nil {
			return invalid("", "remains requires duration and forbids timeout")
		}
	}
	return nil
}

func uniqueStrings(values []string) error {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			return invalid("", "values must be nonempty and distinct")
		}
		seen[value] = true
	}
	return nil
}

func checkFormat(format, value string) error {
	switch format {
	case "prefix":
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
			return invalid("", "expected a canonical network prefix without host bits")
		}
	case formatMAC:
		mac, err := net.ParseMAC(value)
		if err != nil || len(mac) != 6 {
			return invalid("", "expected a six-byte MAC address")
		}
	}
	return nil
}

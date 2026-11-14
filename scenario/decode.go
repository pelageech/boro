package scenario

import (
	"fmt"
	"math"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	AddressType  = reflect.TypeFor[Address]()
	durationType = reflect.TypeFor[Duration]()
	nodeType     = reflect.TypeFor[yaml.Node]()
)

const yamlString = "!!str"

// UnmarshalYAML parses an address literal or the sole supported reference form.
func (a *Address) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag != yamlString {
			return invalid("", "address must be an IP string")
		}
		ip, err := netip.ParseAddr(n.Value)
		if err != nil || ip.Zone() != "" || ip.Is4In6() {
			return invalid("", "invalid IP address")
		}
		*a = Address{Literal: n.Value}
		return nil
	case yaml.MappingNode:
		if len(n.Content) != 2 || n.Content[0].Value != "env" || n.Content[1].Tag != yamlString {
			return invalid("", "address reference must be exactly {env: addresses.name}")
		}
		ref := n.Content[1].Value
		if !strings.HasPrefix(ref, "addresses.") || !ValidName(strings.TrimPrefix(ref, "addresses.")) {
			return invalid("", "address reference must name addresses.name")
		}
		*a = Address{Env: ref}
		return nil
	default:
		return invalid("", "expected an IP literal or environment address reference")
	}
}

// UnmarshalYAML accepts positive Go durations only.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.Tag != yamlString {
		return invalid("", "expected a positive Go duration string")
	}
	value, err := time.ParseDuration(n.Value)
	if err != nil || value <= 0 {
		return invalid("", "expected a positive Go duration")
	}
	*d = Duration(value)
	return nil
}

func decodeNode[T any](n *yaml.Node) (T, error) {
	var out T
	if err := checkShape(n, reflect.TypeFor[T]()); err != nil {
		return out, err
	}
	if err := n.Decode(&out); err != nil {
		return out, fmt.Errorf("decode typed payload: %w", err)
	}
	return out, nil
}

func checkShape(n *yaml.Node, t reflect.Type) error {
	if t.Kind() == reflect.Pointer {
		return checkShape(n, t.Elem())
	}
	switch t {
	case nodeType:
		return nil // Discriminated payloads are decoded by their registration.
	case AddressType:
		var value Address
		return value.UnmarshalYAML(n)
	case durationType:
		var value Duration
		return value.UnmarshalYAML(n)
	}
	switch t.Kind() {
	case reflect.Struct:
		return checkMapping(n, t)
	case reflect.Map:
		if n.Kind != yaml.MappingNode {
			return invalid("", "expected a mapping")
		}
		for i := 1; i < len(n.Content); i += 2 {
			if err := checkShape(n.Content[i], t.Elem()); err != nil {
				return PrefixIssue(n.Content[i-1].Value, err)
			}
		}
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode {
			return invalid("", "expected a sequence")
		}
		for i, child := range n.Content {
			if err := checkShape(child, t.Elem()); err != nil {
				return PrefixIssue(fmt.Sprintf("[%d]", i), err)
			}
		}
	default:
		return checkScalar(n, t)
	}
	return nil
}

func checkMapping(n *yaml.Node, t reflect.Type) error {
	if n.Kind != yaml.MappingNode {
		return invalid("", "expected a mapping")
	}
	fields := make(map[string]reflect.StructField, t.NumField())
	for f := range t.Fields() {
		if f.IsExported() && yamlFieldName(f) != "-" {
			name := yamlFieldName(f)
			fields[name] = f
		}
	}
	seen := make(map[string]bool, len(n.Content)/2)
	for i := 0; i < len(n.Content); i += 2 {
		name, child := n.Content[i].Value, n.Content[i+1]
		f, exists := fields[name]
		if !exists {
			return invalid(name, "unknown field")
		}
		seen[name] = true
		if err := checkShape(child, f.Type); err != nil {
			return PrefixIssue(name, err)
		}
		if err := checkField(child, f); err != nil {
			return PrefixIssue(name, err)
		}
	}
	for f := range t.Fields() {
		name := yamlFieldName(f)
		if strings.Contains(f.Tag.Get("rule"), "required") && !seen[name] {
			return invalid(name, "required field is missing")
		}
	}
	return nil
}

func checkScalar(n *yaml.Node, t reflect.Type) error {
	expected := "!!int"
	switch t.Kind() {
	case reflect.String:
		expected = yamlString
	case reflect.Bool:
		expected = "!!bool"
	case reflect.Float32, reflect.Float64:
		expected = "!!float"
	}
	if n.Kind != yaml.ScalarNode || n.Tag != expected {
		return invalid("", "expected "+strings.TrimPrefix(expected, "!!"))
	}
	value := reflect.New(t)
	if err := n.Decode(value.Interface()); err != nil {
		return invalid("", "value is outside the declared scalar type: "+err.Error())
	}
	if (t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64) && (math.IsNaN(value.Elem().Float()) || math.IsInf(value.Elem().Float(), 0)) {
		return invalid("", "expected a finite float")
	}
	return nil
}

func checkField(n *yaml.Node, f reflect.StructField) error {
	rules := f.Tag.Get("rule")
	if strings.Contains(rules, "nonempty") && len(n.Content) == 0 && n.Kind == yaml.SequenceNode {
		return invalid("", "sequence must be nonempty")
	}
	if strings.Contains(rules, "required") && !slices.Contains(strings.Split(rules, ","), "empty") && n.Tag == yamlString && n.Value == "" {
		return invalid("", "string must be nonempty")
	}
	nodes := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		nodes = n.Content
	}
	for _, child := range nodes {
		if enum := f.Tag.Get("enum"); enum != "" && !slices.Contains(strings.Split(enum, "|"), child.Value) {
			return invalid("", "expected one of "+enum)
		}
		if err := checkRange(child, f); err != nil {
			return err
		}
	}
	return nil
}

func checkRange(n *yaml.Node, f reflect.StructField) error {
	for _, bound := range []string{"min", "max"} {
		text := f.Tag.Get(bound)
		if text == "" {
			continue
		}
		limit, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid schema bound: %w", err)
		}
		var number uint64
		if err := n.Decode(&number); err != nil {
			return invalid("", "integer outside allowed range")
		}
		if (bound == "min" && number < limit) || (bound == "max" && number > limit) {
			return invalid("", "integer violates "+bound+" "+text)
		}
	}
	return nil
}

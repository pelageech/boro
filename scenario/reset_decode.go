package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ReadResetBaseline decodes the complete strict wire contract, retaining its hash.
// Execution availability and binding completeness are checked before Reset effects.
func (s Schema) ReadResetBaseline(reader io.Reader) (ResetBaseline, error) {
	data, err := io.ReadAll(io.LimitReader(reader, DocumentLimit+1))
	if err != nil {
		return ResetBaseline{}, fmt.Errorf("read reset baseline: %w", err)
	}
	wire, err := StrictResetJSON[resetBaselineWire](data)
	if err != nil {
		return ResetBaseline{}, err
	}
	baseline := ResetBaseline{
		Version: wire.Version, ID: wire.ID, Profile: wire.Profile, Registry: wire.Registry,
		Domains: wire.Domains, Assets: wire.Assets, ExpectedChecks: make([]ExpectedCheck, 0, len(wire.ExpectedChecks)), schema: s,
	}
	for _, row := range wire.ExpectedChecks {
		check, err := s.decodeResetCheck(row)
		if err != nil {
			return ResetBaseline{}, PrefixIssue("expected_checks."+row.ID, err)
		}
		baseline.ExpectedChecks = append(baseline.ExpectedChecks, check)
	}
	if err := s.ValidateResetBaseline(baseline); err != nil {
		return ResetBaseline{}, err
	}
	baseline.sha256 = Digest(data)
	canonical, err := json.Marshal(baseline)
	if err != nil {
		return ResetBaseline{}, fmt.Errorf("capture reset baseline fields: %w", err)
	}
	baseline.fieldsSHA256 = Digest(canonical)
	return baseline, nil
}

type resetBaselineWire struct {
	Version        int              `json:"version"`
	ID             string           `json:"id" format:"id"`
	Profile        string           `json:"profile"`
	Registry       string           `json:"registry"`
	Domains        []ResetDomain    `json:"domains"`
	Assets         []ResetAsset     `json:"assets"`
	ExpectedChecks []resetCheckWire `json:"expected_checks"`
}

type resetCheckWire struct {
	ID       string          `json:"id" format:"id"`
	Domain   string          `json:"domain"`
	Target   ResetTarget     `json:"target"`
	Kind     string          `json:"kind"`
	Scope    json.RawMessage `json:"scope"`
	Expected json.RawMessage `json:"expected"`
}

func (s Schema) decodeResetCheck(wire resetCheckWire) (ExpectedCheck, error) {
	scopeType, expectedType, _, err := s.ResetCheckTypes(wire.Kind)
	if err != nil {
		return ExpectedCheck{}, err
	}
	scope, err := s.DecodeResetValue(wire.Scope, scopeType)
	if err != nil {
		return ExpectedCheck{}, PrefixIssue("scope", err)
	}
	expected, err := s.DecodeResetValue(wire.Expected, expectedType)
	if err != nil {
		return ExpectedCheck{}, PrefixIssue("expected", err)
	}
	return ExpectedCheck{ID: wire.ID, Domain: wire.Domain, Target: wire.Target, Kind: wire.Kind, Scope: scope, Expected: expected}, nil
}

func StrictResetJSON[T any](data []byte) (T, error) {
	var zero T
	if len(data) > DocumentLimit || !utf8.Valid(data) {
		return zero, invalid("reset", "document exceeds limit or is not UTF-8")
	}
	value, err := ReadJSON[T](bytes.NewReader(data))
	if err != nil {
		return zero, err
	}
	if err := (Schema{}).resetJSONShape(data, reflect.TypeFor[T](), reflect.StructField{}); err != nil {
		return zero, err
	}
	return value, nil
}

func (s Schema) DecodeResetValue(data []byte, shape reflect.Type) (ResetValue, error) {
	if err := s.resetJSONShape(data, shape, reflect.StructField{}); err != nil {
		return nil, err
	}
	out := reflect.New(shape)
	if err := json.Unmarshal(data, out.Interface()); err != nil {
		return nil, fmt.Errorf("decode reset value: %w", err)
	}
	return out.Elem().Interface(), nil
}

func ResetTyped[T ResetValue](value ResetValue) (T, error) {
	out, ok := value.(T)
	if !ok {
		return out, invalid("reset", "wrong concrete reset payload type")
	}
	return out, nil
}

func (s Schema) resetJSONShape(data []byte, shape reflect.Type, field reflect.StructField) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return invalid("reset", "missing or null value")
	}
	if shape.Kind() == reflect.Pointer {
		return s.resetJSONShape(data, shape.Elem(), field)
	}
	switch shape {
	case reflect.TypeFor[json.RawMessage]():
		return nil // The discriminator's concrete decoder must consume this value.
	case reflect.TypeFor[time.Time]():
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return fmt.Errorf("reset timestamp: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil || parsed.IsZero() || !strings.HasSuffix(text, "Z") {
			return invalid("time", "expected a nonzero UTC RFC3339 timestamp ending in Z")
		}
		return nil
	}
	switch shape.Kind() {
	case reflect.Struct:
		return s.resetJSONObject(data, shape)
	case reflect.Slice:
		var elements []json.RawMessage
		if err := json.Unmarshal(data, &elements); err != nil {
			return fmt.Errorf("reset array: %w", err)
		}
		for i, element := range elements {
			if err := s.resetJSONShape(element, shape.Elem(), field); err != nil {
				return PrefixIssue(fmt.Sprintf("[%d]", i), err)
			}
		}
		return nil
	case reflect.String:
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return fmt.Errorf("reset string: %w", err)
		}
		return s.resetString(text, field)
	case reflect.Bool:
		if string(data) != "true" && string(data) != "false" {
			return invalid("reset", "expected a JSON boolean")
		}
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return resetInteger(data, shape, field)
	default:
		return invalid("reset", "no closed JSON shape for "+shape.String())
	}
}

func (s Schema) resetJSONObject(data []byte, shape reflect.Type) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("reset object: %w", err)
	}
	for field := range shape.Fields() {
		if !field.IsExported() {
			continue
		}
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" {
			name = field.Name
		}
		if name == "-" {
			continue
		}
		raw, exists := fields[name]
		if !exists {
			if options == jsonOmitEmpty {
				continue
			}
			return invalid(name, "required field is missing")
		}
		if err := s.resetJSONShape(raw, field.Type, field); err != nil {
			return PrefixIssue(name, err)
		}
		delete(fields, name)
	}
	if len(fields) != 0 {
		return invalid("reset", "unknown object fields")
	}
	return nil
}

func resetInteger(data []byte, shape reflect.Type, field reflect.StructField) error {
	value := reflect.New(shape)
	if err := json.Unmarshal(data, value.Interface()); err != nil {
		return fmt.Errorf("reset integer: %w", err)
	}
	number, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil {
		return invalid("reset", "expected a nonnegative JSON integer")
	}
	for _, bound := range []string{resetMin, resetMax} {
		text := field.Tag.Get(bound)
		if text == "" {
			continue
		}
		limit, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return fmt.Errorf("reset schema bound: %w", err)
		}
		if (bound == resetMin && number < limit) || (bound == resetMax && number > limit) {
			return invalid("reset", "integer violates "+bound+" "+text)
		}
	}
	return nil
}

func (s Schema) resetString(text string, field reflect.StructField) error {
	if text == "" && field.Tag.Get("empty") != "true" {
		return invalid("reset", "empty strings are forbidden")
	}
	if enum := field.Tag.Get("enum"); enum != "" && !slices.Contains(strings.Split(enum, "|"), text) {
		return invalid("reset", "expected one of "+enum)
	}
	return s.resetProtocolString(field.Tag.Get("format"), text)
}

func (s Schema) resetProtocolString(format, text string) error {
	switch format {
	case "id":
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`).MatchString(text) {
			return invalid("reset", "invalid ID")
		}
	case "sha256":
		length := 64
		if len(text) != length || strings.Trim(text, "0123456789abcdef") != "" {
			return invalid("reset", "invalid lowercase hex identity")
		}
	case "ip":
		ip, err := netip.ParseAddr(text)
		if err != nil || ip.Zone() != "" || ip.Is4In6() || ip.String() != text {
			return invalid("reset", "noncanonical IP")
		}
	case "cidr":
		prefix, err := netip.ParsePrefix(text)
		if err != nil || prefix != prefix.Masked() || prefix.String() != text || prefix.Addr().Is4In6() {
			return invalid("reset", "noncanonical CIDR")
		}
	case "mac":
		mac, err := net.ParseMAC(text)
		if err != nil || len(mac) != 6 || mac.String() != text {
			return invalid("reset", "noncanonical MAC")
		}
	default:
		if format == "" {
			return nil
		}
		validate, ok := s.LookupFormat("reset." + format)
		if !ok {
			validate, ok = s.LookupFormat(format)
		}
		if !ok {
			return invalid("reset", "unsupported reset format "+format)
		}
		return validate(text)
	}
	return nil
}

// ResetString validates generic reset envelope scalars.
func ResetString(text string, field reflect.StructField) error {
	return (Schema{}).resetString(text, field)
}

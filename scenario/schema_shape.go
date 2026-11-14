package scenario

import (
	"encoding"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	formatMAC     = "mac"
	jsonOmitEmpty = "omitempty"
)

// validateShape admits only finite, strictly decoded data trees. Doing this at
// registration keeps unsupported extension types out of loading and acquisition.
func (s Schema) validateShape(t reflect.Type, visiting map[reflect.Type]bool) error {
	if visiting[t] {
		return invalid("", "recursive payload shapes are unsupported")
	}
	switch t {
	case AddressType, durationType:
		return nil
	}
	if t.Kind() == reflect.Pointer {
		visiting[t] = true
		defer delete(visiting, t)
		return s.validateShape(t.Elem(), visiting)
	}
	for _, codec := range []reflect.Type{
		reflect.TypeFor[yaml.Unmarshaler](), reflect.TypeFor[yaml.Marshaler](),
		reflect.TypeFor[interface{ UnmarshalYAML(func(any) error) error }](),
		reflect.TypeFor[json.Unmarshaler](), reflect.TypeFor[json.Marshaler](),
		reflect.TypeFor[encoding.TextUnmarshaler](), reflect.TypeFor[encoding.TextMarshaler](),
	} {
		if t.Implements(codec) || reflect.PointerTo(t).Implements(codec) {
			return invalid("", "custom payload codecs are unsupported")
		}
	}
	if ref, ok := reflect.TypeAssert[resourceReference](reflect.Zero(t)); ok {
		if t.Kind() != reflect.String || (!s.standalone && s.resourceName(ref.resourceType()) == "") {
			return invalid("", "typed reference requires a registered resource struct")
		}
		return nil
	}
	visiting[t] = true
	defer delete(visiting, t)
	switch t.Kind() {
	case reflect.Slice:
		return s.validateShape(t.Elem(), visiting)
	case reflect.Map:
		if t.Key() != reflect.TypeFor[string]() {
			return invalid("", "payload maps require string keys")
		}
		return s.validateShape(t.Elem(), visiting)
	case reflect.Struct:
		return s.validateStruct(t, visiting)
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return nil
	default:
		return invalid("", "unsupported payload shape "+t.String())
	}
}

func (s Schema) validateStruct(t reflect.Type, visiting map[reflect.Type]bool) error {
	names := make(map[string]bool)
	for field := range t.Fields() {
		if !field.IsExported() {
			return invalid(field.Name, "payload fields must be exported")
		}
		name := yamlFieldName(field)
		if name != "-" {
			if names[name] {
				return invalid(name, "duplicate YAML field")
			}
			names[name] = true
			if err := s.validateField(field); err != nil {
				return PrefixIssue(name, err)
			}
		}
		if err := s.validateShape(field.Type, visiting); err != nil {
			return PrefixIssue(name, err)
		}
	}
	return nil
}

func yamlFieldName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
	if name == "" {
		return strings.ToLower(field.Name)
	}
	return name
}

func (s Schema) validateField(field reflect.StructField) error {
	valueType := field.Type
	for valueType.Kind() == reflect.Pointer || valueType.Kind() == reflect.Slice || valueType.Kind() == reflect.Map {
		valueType = valueType.Elem()
	}
	options := strings.Split(field.Tag.Get("yaml"), ",")
	for _, option := range options[1:] {
		if option != jsonOmitEmpty && option != "flow" {
			return invalid("", "unsupported YAML field option "+option)
		}
	}
	if field.Anonymous {
		return invalid("", "embedded payload fields require yaml:\"-\"")
	}
	for rule := range strings.SplitSeq(field.Tag.Get("rule"), ",") {
		if !slices.Contains([]string{"", "required", "nonempty", "empty"}, rule) {
			return invalid("", "unsupported field rule "+rule)
		}
	}
	for _, bound := range []string{resetMin, resetMax} {
		if text := field.Tag.Get(bound); text != "" {
			if _, err := strconv.ParseUint(text, 10, 64); err != nil {
				return invalid("", "invalid schema bound "+bound)
			}
			if valueType.Kind() < reflect.Int || valueType.Kind() > reflect.Uint64 {
				return invalid("", "schema bounds require integer fields")
			}
		}
	}
	if format := field.Tag.Get("format"); format != "" {
		if _, ok := s.formatEntries()[format]; !ok {
			return invalid("", "unsupported field format "+format)
		}
		if valueType.Kind() != reflect.String {
			return invalid("", "formats require string fields")
		}
	}
	return s.validateReferenceField(field)
}

func (s Schema) validateReferenceField(field reflect.StructField) error {
	kind := field.Tag.Get("ref")
	if kind == "" {
		return nil
	}
	if _, ok := s.LookupResource(kind); !ok && !s.standalone {
		return invalid("", "unknown resource reference kind "+kind)
	}
	t := field.Type
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.String {
		return invalid("", "resource reference fields must contain strings")
	}
	if ref, ok := reflect.TypeAssert[resourceReference](reflect.Zero(t)); ok && !s.standalone && s.resourceName(ref.resourceType()) != kind {
		return invalid("", "resource reference tag conflicts with its typed resource")
	}
	return nil
}

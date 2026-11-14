package scenario

import (
	"encoding"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

func (s Schema) validateResetShape(t reflect.Type, visiting map[reflect.Type]bool) error {
	if visiting[t] {
		return invalid("", "recursive reset shapes are unsupported")
	}
	for _, codec := range []reflect.Type{
		reflect.TypeFor[json.Marshaler](), reflect.TypeFor[json.Unmarshaler](),
		reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[encoding.TextUnmarshaler](),
	} {
		if t.Implements(codec) || reflect.PointerTo(t).Implements(codec) {
			return invalid("", "custom reset codecs are unsupported")
		}
	}
	visiting[t] = true
	defer delete(visiting, t)
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice:
		return s.validateResetShape(t.Elem(), visiting)
	case reflect.Struct:
		return s.validateResetStruct(t, visiting)
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return nil
	default:
		return invalid("", "unsupported reset data shape "+t.String())
	}
}

func (s Schema) validateResetStruct(t reflect.Type, visiting map[reflect.Type]bool) error {
	names := make(map[string]bool)
	for field := range t.Fields() {
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" {
			name = field.Name
		}
		if !field.IsExported() || field.Anonymous || name == "-" ||
			(options != "" && options != jsonOmitEmpty) || names[name] {
			return invalid(field.Name, "reset fields require unique JSON names and exported data")
		}
		names[name] = true
		for _, bound := range []string{resetMin, resetMax} {
			if text := field.Tag.Get(bound); text != "" {
				if _, err := strconv.ParseUint(text, 10, 64); err != nil {
					return invalid(name, "invalid reset schema bound")
				}
			}
		}
		format := field.Tag.Get("format")
		if !slices.Contains([]string{"", "id", "sha256", "ip", "cidr", "mac"}, format) {
			_, plain := s.LookupFormat(format)
			_, reset := s.LookupFormat("reset." + format)
			if !plain && !reset {
				return invalid(name, "unsupported reset format")
			}
		}
		if err := s.validateResetShape(field.Type, visiting); err != nil {
			return PrefixIssue(name, err)
		}
	}
	return nil
}

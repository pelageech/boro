package scenario

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

func (s Schema) validateDataTree(v reflect.Value, path string, field reflect.StructField) error {
	if v.Type() == AddressType {
		address, _ := reflect.TypeAssert[Address](v)
		if err := address.Validate(); err != nil {
			return PrefixIssue(path, err)
		}
		return nil
	}
	if err := s.validateDataField(v, field); err != nil {
		return PrefixIssue(path, err)
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return s.validateDataTree(v.Elem(), path, field)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			f := v.Type().Field(i)
			if f.IsExported() && yamlFieldName(f) != "-" {
				if err := s.validateDataTree(v.Field(i), path+"."+yamlFieldName(f), f); err != nil {
					return err
				}
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			if err := s.validateDataTree(v.Index(i), fmt.Sprintf("%s[%d]", path, i), field); err != nil {
				return err
			}
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			if err := s.validateDataTree(v.MapIndex(key), path+"."+key.String(), field); err != nil {
				return err
			}
		}
	}
	return nil
}

// Go values have scalar presence inherently. Nil pointers and collections still
// represent omission, and non-nil empty collections retain their explicit value.
func (s Schema) validateDataField(v reflect.Value, field reflect.StructField) error {
	rules := strings.Split(field.Tag.Get("rule"), ",")
	required := slices.Contains(rules, "required")
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		if v.IsNil() {
			if required {
				return invalid("", "required field is missing")
			}
			return nil
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return nil
	case reflect.Slice, reflect.Map:
		if slices.Contains(rules, "nonempty") && v.Len() == 0 {
			return invalid("", "sequence must be nonempty")
		}
		return nil
	}
	if v.Type() == durationType {
		if v.Int() <= 0 {
			return invalid("", "expected a positive Go duration")
		}
		return nil
	}
	if v.Kind() == reflect.Float32 || v.Kind() == reflect.Float64 {
		if math.IsNaN(v.Float()) || math.IsInf(v.Float(), 0) {
			return invalid("", "expected a finite float")
		}
	}
	// An optional Go string does not distinguish omission from explicit "".
	if v.Kind() == reflect.String && v.String() == "" && !required &&
		field.Type.Kind() != reflect.Slice && field.Type.Kind() != reflect.Map {
		return nil
	}
	if v.Kind() == reflect.Struct {
		return nil
	}
	// Use the decoder's scalar constraints for native values too. Presence is
	// handled above because Go scalars cannot represent omission.
	var node yaml.Node
	if err := node.Encode(v.Interface()); err != nil {
		return fmt.Errorf("encode scalar constraints: %w", err)
	}
	if err := checkField(&node, field); err != nil {
		return err
	}
	if format := field.Tag.Get("format"); format != "" && v.Kind() == reflect.String && v.String() != "" {
		validate, ok := s.formatEntries()[format]
		if !ok {
			return invalid("", "unsupported field format "+format)
		}
		return validate(v.String())
	}
	return nil
}

// ValidateFields applies scalar, format and presence constraints to detached native data.
// Resource and profile relationships are checked by ValidateDefinition.
func (s Schema) ValidateFields(value any) error {
	if value == nil {
		return invalid("", "native data must not be nil")
	}
	return s.validateDataTree(reflect.ValueOf(value), "", reflect.StructField{})
}

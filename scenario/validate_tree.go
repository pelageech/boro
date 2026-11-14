package scenario

import (
	"fmt"
	"reflect"
	"sort"
)

type treeValidation struct {
	definition        Definition
	profile           ProfileDeclaration
	capability        string
	schema            Schema
	validateSelectors bool
}

func (t treeValidation) walk(v reflect.Value, path string, field reflect.StructField) error {
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return nil
	}
	if terminal, err := t.validateNode(v, path); terminal || err != nil {
		return err
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		return t.walk(v.Elem(), path, field)
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			f := v.Type().Field(i)
			if !f.IsExported() || yamlFieldName(f) == "-" {
				continue
			}
			name := yamlFieldName(f)
			if err := t.walk(v.Field(i), path+"."+name, f); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			if err := t.walk(v.Index(i), fmt.Sprintf("%s[%d]", path, i), field); err != nil {
				return err
			}
		}
	case reflect.Map:
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, key := range keys {
			if err := t.walk(v.MapIndex(key), path+"."+key.String(), field); err != nil {
				return err
			}
		}
	case reflect.String:
		return t.reference(v, path, field)
	}
	return nil
}

func (t treeValidation) validateNode(v reflect.Value, path string) (bool, error) {
	if validate := t.schema.validators()[v.Type()]; validate != nil {
		if err := validate(Clone(ValidationContext{Definition: t.definition, Profile: t.profile}), Clone(v.Interface())); err != nil {
			return false, PrefixIssue(path, err)
		}
	}
	switch value := v.Interface().(type) {
	case Address:
		if _, err := t.profile.AddressFamily(value); err != nil {
			return true, PrefixIssue(path, err)
		}
		return true, nil
	case Selector:
		if err := value.Validate(); err != nil {
			return true, PrefixIssue(path, err)
		}
		if !t.validateSelectors {
			return true, nil
		}
		if _, err := t.profile.SelectServices(value, t.capability); err != nil {
			return true, PrefixIssue(path, err)
		}
		return true, nil
	case Wait:
		if err := value.Validate(); err != nil {
			return true, PrefixIssue(path, err)
		}
		return true, nil
	default:
		return false, nil
	}
}

func (t treeValidation) reference(v reflect.Value, path string, field reflect.StructField) error {
	value := v.String()
	// Empty scalars may be omitted optional fields; sequence members are present.
	if value == "" && field.Type.Kind() != reflect.Slice && field.Type.Kind() != reflect.Map {
		return nil
	}
	kind := field.Tag.Get("ref")
	if ref, ok := reflect.TypeAssert[resourceReference](v); ok {
		kind = t.schema.resourceName(ref.resourceType())
	}
	if kind != "" {
		resource, exists := t.definition.Resources[Name(value)]
		descriptor, registered := t.schema.LookupResource(kind)
		if !exists || !registered || resource.Type != kind || reflect.TypeOf(resource.Spec) != descriptor.Type {
			return invalid(path, "expected resource of type "+kind+": "+value)
		}
	}
	if field.Tag.Get("service") != "" {
		if _, err := t.profile.SelectServices(Selector{Service: value}, ""); err != nil {
			return PrefixIssue(path, err)
		}
	}
	return nil
}

func (t treeValidation) payload(value Payload, path string) error {
	if err := t.schema.validateDataTree(reflect.ValueOf(value), path, reflect.StructField{}); err != nil {
		return err
	}
	return t.walk(reflect.ValueOf(value), path, reflect.StructField{})
}

func (t treeValidation) validateResource(r Resource) error { return t.payload(r.Spec, "") }

func (t treeValidation) operation(step Step, reg Descriptor) error {
	value := reflect.ValueOf(step.Input)
	on := value.FieldByName("On")
	if reg.Role != "" && (!on.IsValid() || (on.Kind() == reflect.Pointer && on.IsNil())) {
		matches, err := t.profile.SelectServices(Selector{Role: reg.Role, Match: MatchAll}, reg.Capability)
		if err != nil {
			return PrefixIssue("with.on", err)
		}
		if len(matches) != 1 {
			return invalid("with.on", "omitted selector requires a single unambiguous "+reg.Role+" role")
		}
	}
	return nil
}

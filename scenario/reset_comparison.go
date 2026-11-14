package scenario

import (
	"reflect"
	"slices"
)

func (s Schema) CompareResetValue(check ExpectedCheck, actual any) (bool, error) {
	d, exists := s.resetCheckEntries()[check.Kind]
	if !exists {
		return false, invalid("kind", "unknown reset check "+check.Kind)
	}
	if reflect.TypeOf(check.Expected) != d.Expected {
		return false, invalid("expected", "wrong concrete expected type")
	}
	if reflect.TypeOf(actual) != d.Actual {
		return false, invalid("actual", "wrong concrete type for expected check")
	}
	return d.Compare(Clone(check.Expected), Clone(actual))
}

// EqualResetInventory compares inventory multisets, preserving configured ordered fields.
func EqualResetInventory(left, right any, orderedFields []string) bool {
	if reflect.TypeOf(left) != reflect.TypeOf(right) {
		return false
	}
	return equalReset(reflect.ValueOf(left), reflect.ValueOf(right), "", orderedFields)
}

func equalReset(left, right reflect.Value, field string, orderedFields []string) bool {
	if left.Type() != right.Type() {
		return false
	}
	switch left.Kind() {
	case reflect.Pointer:
		return (left.IsNil() && right.IsNil()) ||
			(!left.IsNil() && !right.IsNil() && equalReset(left.Elem(), right.Elem(), field, orderedFields))
	case reflect.Struct:
		return equalResetStruct(left, right, orderedFields)
	case reflect.Slice:
		return equalResetSlice(left, right, field, orderedFields)
	default:
		return reflect.DeepEqual(left.Interface(), right.Interface())
	}
}

func equalResetStruct(left, right reflect.Value, orderedFields []string) bool {
	for i := range left.NumField() {
		if left.Type().Field(i).IsExported() && !equalReset(left.Field(i), right.Field(i), left.Type().Field(i).Name, orderedFields) {
			return false
		}
	}
	return true
}

func equalResetSlice(left, right reflect.Value, field string, orderedFields []string) bool {
	if left.Len() != right.Len() {
		return false
	}
	if slices.Contains(orderedFields, field) {
		for i := range left.Len() {
			if !equalReset(left.Index(i), right.Index(i), "", orderedFields) {
				return false
			}
		}
		return true
	}
	used := make([]bool, right.Len())
	for i := range left.Len() {
		found := false
		for j := range right.Len() {
			if !used[j] && equalReset(left.Index(i), right.Index(j), "", orderedFields) {
				used[j], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

package azurearm

import (
	"reflect"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
)

// This reference-enumeration logic is adapted from Azure Service Operator's
// internal/reflecthelpers package (FindResourceReferences), which cannot be
// imported directly because it lives under the module's internal/ tree.
//
// Source: github.com/Azure/azure-service-operator/v2 (MIT License).
// We reimplement the minimal recursive walk we need and replace ASO's internal
// set.Set with a plain map to avoid pulling in additional internal packages.

// findResourceReferences walks an arbitrary ASO spec value via reflection and
// collects every genruntime.ResourceReference it contains (including nested
// structs, pointers, slices and maps).
func findResourceReferences(obj any) map[genruntime.ResourceReference]struct{} {
	result := make(map[genruntime.ResourceReference]struct{})

	collectReferences(reflect.ValueOf(obj), result)

	return result
}

func collectReferences(value reflect.Value, result map[genruntime.ResourceReference]struct{}) {
	//nolint:exhaustive // only composite kinds can contain references; the default handles the rest
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			collectReferences(value.Elem(), result)
		}
	case reflect.Struct:
		collectFromStruct(value, result)
	case reflect.Slice, reflect.Array:
		for i := range value.Len() {
			collectReferences(value.Index(i), result)
		}
	case reflect.Map:
		for _, key := range value.MapKeys() {
			collectReferences(value.MapIndex(key), result)
		}
	default:
	}
}

func collectFromStruct(value reflect.Value, result map[genruntime.ResourceReference]struct{}) {
	if value.Type() == reflect.TypeFor[genruntime.ResourceReference]() {
		reference, ok := value.Interface().(genruntime.ResourceReference)
		if ok {
			result[reference] = struct{}{}
		}

		return
	}

	for field, fieldValue := range value.Fields() {
		if field.IsExported() {
			collectReferences(fieldValue, result)
		}
	}
}

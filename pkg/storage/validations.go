package storage

import (
	"fmt"
)

// FieldIsNonNull validates that the given value is not nil.
func FieldIsNonNull(field string, _ bool) []string {
	var allErrs []string
	if field == "" {
		allErrs = append(allErrs, fmt.Sprintf("%s: %s", field, ErrFieldIsNull))
	}

	return allErrs
}

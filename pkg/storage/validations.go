// Package storage provides validation functions for storage operations.
package storage

import (
	"fmt"
)

var ValidateNonNullField = fieldIsNonNull

// fieldIsNonNull validates that the given value is not nil.
func fieldIsNonNull(field string, prefix bool) []string {
	var allErrs []string
	if field == "" {
		allErrs = append(allErrs, fmt.Sprintf("%s: %s", field, ErrFieldIsNull))
	}
	return allErrs
}
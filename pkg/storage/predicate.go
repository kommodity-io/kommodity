// Package storage provides validation functions for storage operations.
package storage

import (
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

// PredicateFunc returns a selection predicate function for the given getAttrs function.
//
//nolint:lll // Cannot be broken into multiple lines.
func PredicateFunc(getAttrs func(obj runtime.Object) (labels.Set, fields.Set, error)) func(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return func(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
		return storage.SelectionPredicate{
			Label:    label,
			Field:    field,
			GetAttrs: getAttrs,
		}
	}
}

// NamespacedPredicateFunc returns a selection predicate function.
func NamespacedPredicateFunc() func(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return func(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
		return storage.SelectionPredicate{
			Label:    label,
			Field:    field,
			GetAttrs: storage.DefaultNamespaceScopedAttr,
		}
	}
}

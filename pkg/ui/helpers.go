package ui

import (
	"errors"
	"html/template"
	"strings"
)

var (
	// ErrDictOddArgs is returned when dict function receives an odd number of arguments.
	ErrDictOddArgs = errors.New("dict expects an even number of arguments")
	// ErrDictNonStringKey is returned when dict function receives a non-string key.
	ErrDictNonStringKey = errors.New("dict keys must be strings")
)

// templateFuncs returns the function map for template rendering.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"lower": strings.ToLower,
		"add": func(a int, b int) int {
			return a + b
		},
		"mul": func(a int, b int) int {
			return a * b
		},
		"dict": func(values ...any) (map[string]any, error) {
			const pairSize = 2
			if len(values)%pairSize != 0 {
				return nil, ErrDictOddArgs
			}

			dict := make(map[string]any, len(values)/pairSize)

			for idx := 0; idx < len(values); idx += pairSize {
				key, ok := values[idx].(string)
				if !ok {
					return nil, ErrDictNonStringKey
				}

				dict[key] = values[idx+1]
			}

			return dict, nil
		},
	}
}

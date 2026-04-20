package ui

import (
	"html/template"
	"strings"
)

// templateFuncs returns the function map for template rendering.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"lower": strings.ToLower,
		"mul": func(a int, b int) int {
			return a * b
		},
	}
}

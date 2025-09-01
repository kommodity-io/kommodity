package main

//go:generate go run .

// This file is responsible for generating the Kubernetes API scheme for the providers defined in pkg/provider/providers.yaml
// The generated file is located at pkg/provider/scheme.go

import (
	"log"
	"os"
	"text/template"

	"gopkg.in/yaml.v3"
)

const providerFolder = "../provider/"

var providersFile = providerFolder + "providers.yaml"
var generatedGoFile = providerFolder + "scheme.go"
var templatefile = "schemes.tmpl"

type Provider struct {
	Repository      string   `yaml:"repository"`
	GoModule        string   `yaml:"go_module"`
	SchemeLocations []string `yaml:"scheme_locations"`
}

type ProvidersList struct {
	Providers []Provider `yaml:"providers"`
}

func main() {
	// Load providers.yaml
	providersyaml, err := os.ReadFile(providersFile)
	if err != nil {
		log.Fatal(err)
	}

	// Load schemes.tmpl
	tpl, err := os.ReadFile(templatefile)
	if err != nil {
		log.Fatal(err)
	}

	var cfg ProvidersList

	if err := yaml.Unmarshal(providersyaml, &cfg); err != nil {
		log.Fatal(err)
	}

	tmpl, err := template.New("gen").Funcs(template.FuncMap{
		"increment": func(a int) int { return a + 1 },
	}).Parse(string(tpl))
	if err != nil {
		log.Fatal(err)
	}

	f, err := os.Create(generatedGoFile)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, cfg); err != nil {
		log.Fatal(err)
	}
}

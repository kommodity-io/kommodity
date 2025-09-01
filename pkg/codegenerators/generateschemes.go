// This file is responsible for generating the Kubernetes API scheme 
// for the providers defined in pkg/provider/providers.yaml
// The generated file is located at pkg/provider/scheme.go
package main

//go:generate go run .


import (
	"log"
	"os"
	"text/template"

	"gopkg.in/yaml.v3"
)

const providerFolder = "../provider/"

type Provider struct {
	Repository      string   `yaml:"repository"`
	GoModule        string   `yaml:"goModule"`
	SchemeLocations []string `yaml:"schemeLocations"`
}

type ProvidersList struct {
	Providers []Provider `yaml:"providers"`
}

func main() {
	providersFile := providerFolder + "providers.yaml"
	generatedGoFile := providerFolder + "scheme.go"
	templatefile := "schemes.tmpl"

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

	err = yaml.Unmarshal(providersyaml, &cfg)
	if err != nil {
		log.Fatal(err)
	}

	tmpl, err := template.New("gen").Funcs(template.FuncMap{
		"increment": func(a int) int { return a + 1 },
	}).Parse(string(tpl))
	if err != nil {
		log.Fatal(err)
	}

	file, err := os.Create(generatedGoFile)
	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		err := file.Close()
		if err != nil {
			log.Printf("error closing file: %v", err)
		}
	}()

	err = tmpl.Execute(file, cfg)
	if err != nil {
		log.Printf("error executing template: %v", err)
		
		return
	}
}

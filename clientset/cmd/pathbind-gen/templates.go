package main

import _ "embed"

//go:embed templates/helpers.go.tmpl
var helpersTemplate string

//go:embed templates/create.go.tmpl
var createTemplate string

//go:embed templates/update.go.tmpl
var updateTemplate string

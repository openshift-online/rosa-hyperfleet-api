package tf

import _ "embed"

//go:embed templates/state.go.tmpl
var stateTemplate string

//go:embed templates/state_native.go.tmpl
var stateNativeTemplate string

//go:embed templates/resource.go.tmpl
var resourceTemplate string

//go:embed templates/utils_gen.go.tmpl
var utilsGenTemplate string

package openapi

// Generator generates OpenAPI schemas from Go types
type Generator struct {
	// InputDirs are the directories containing Go types to generate schemas for
	InputDirs []string

	// OutputFile is where to write the OpenAPI schema
	OutputFile string

	// Title is the API title
	Title string

	// Version is the API version
	Version string

	// knownTypes tracks which type names we've seen (for $ref generation)
	knownTypes map[string]bool
}

// NewGenerator creates a new OpenAPI generator
func NewGenerator(inputDirs []string, outputFile string) *Generator {
	return &Generator{
		InputDirs:  inputDirs,
		OutputFile: outputFile,
		Title:      "HyperFleet API",
		Version:    "v1alpha1",
	}
}

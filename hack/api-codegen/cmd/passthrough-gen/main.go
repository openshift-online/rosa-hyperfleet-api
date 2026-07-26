package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/markers"
	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/passthrough"
)

//go:embed field_metadata.json
var embeddedRegistry []byte

func main() {
	var (
		sourceDir    string
		importPath   string
		outputDir    string
		typeNames    string
		registryFile string
		packageName  string
		fieldPrefix  string
	)

	flag.StringVar(&sourceDir, "source-dir", "", "Directory containing source Go files (use this OR -import-path)")
	flag.StringVar(&importPath, "import-path", "", "Go import path to resolve via go.mod (use this OR -source-dir)")
	flag.StringVar(&outputDir, "output-dir", "", "Directory for generated output (required)")
	flag.StringVar(&typeNames, "types", "", "Comma-separated list of type names to generate (required)")
	flag.StringVar(&registryFile, "registry", "", "Path to field metadata registry (optional)")
	flag.StringVar(&packageName, "package", "v1alpha1", "Package name for generated code")
	flag.StringVar(&fieldPrefix, "field-prefix", "", "Dotted path prefix for registry lookups (e.g., spec.hostedCluster)")
	flag.Parse()

	// Validate flags
	if outputDir == "" || typeNames == "" {
		flag.Usage()
		os.Exit(1)
	}

	if sourceDir == "" && importPath == "" {
		log.Fatalf("Either -source-dir or -import-path must be specified")
	}

	if sourceDir != "" && importPath != "" {
		log.Fatalf("Cannot specify both -source-dir and -import-path")
	}

	// Parse type names
	types := strings.Split(typeNames, ",")
	for i := range types {
		types[i] = strings.TrimSpace(types[i])
	}

	// Load registry: use explicit file if provided, otherwise use embedded default
	var registry markers.FieldRegistry
	if registryFile != "" {
		log.Printf("Loading field registry from: %s", registryFile)
		var err error
		registry, err = markers.LoadRegistryFromJSON(registryFile)
		if err != nil {
			log.Fatalf("Failed to load registry: %v", err)
		}
		log.Printf("Loaded %d field markers from registry", len(registry))
	} else {
		var err error
		registry, err = markers.LoadRegistryFromJSONBytes(embeddedRegistry)
		if err != nil {
			log.Fatalf("Failed to load embedded registry: %v", err)
		}
		log.Printf("Loaded %d field markers from embedded registry", len(registry))
	}

	// Create generator
	var gen *passthrough.Generator
	var err error

	if importPath != "" {
		log.Printf("Resolving import path: %s", importPath)
		gen, err = passthrough.NewGeneratorFromImportPath(importPath, types, registry)
		if err != nil {
			log.Fatalf("Failed to resolve import path: %v", err)
		}
		log.Printf("Resolved to directory: %s", gen.SourceDir)
	} else {
		gen = passthrough.NewGenerator(sourceDir, types, registry)
	}

	gen.OutputPackage = packageName
	gen.FieldPrefix = fieldPrefix

	// Load source files
	log.Printf("Loading source files from: %s", gen.SourceDir)
	if err := gen.LoadSourceFiles(gen.SourceDir); err != nil {
		log.Fatalf("Failed to load source files: %v", err)
	}

	log.Printf("Loaded %d source files", len(gen.ParsedFiles()))

	// Generate passthrough types
	log.Printf("Generating passthrough types: %v", types)
	if err := gen.Generate(outputDir); err != nil {
		log.Fatalf("Failed to generate: %v", err)
	}

	fmt.Printf("Successfully generated passthrough types in %s\n", outputDir)
}

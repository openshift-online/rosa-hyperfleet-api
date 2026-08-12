package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/markers"
	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/passthrough"
)

func main() {
	var (
		sourceDir     string
		importPath    string
		outputDir     string
		typeNames     string
		registryFile  string
		packageName   string
		fieldPrefix   string
		typeOverrides string
	)

	flag.StringVar(&sourceDir, "source-dir", "", "Directory containing source Go files (use this OR -import-path)")
	flag.StringVar(&importPath, "import-path", "", "Go import path to resolve via go.mod (use this OR -source-dir)")
	flag.StringVar(&outputDir, "output-dir", "", "Directory for generated output (required)")
	flag.StringVar(&typeNames, "types", "", "Comma-separated list of type names to generate (required)")
	flag.StringVar(&registryFile, "registry", "", "Path to field metadata registry (required)")
	flag.StringVar(&packageName, "package", "v1alpha1", "Package name for generated code")
	flag.StringVar(&fieldPrefix, "field-prefix", "", "Dotted path prefix for registry lookups (e.g., spec.hostedCluster)")
	flag.StringVar(&typeOverrides, "type-overrides", "", "Comma-separated type overrides in from=to format (e.g., hypershiftv1beta1.ClusterConfiguration=ClusterConfiguration)")
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

	// Load registry
	if registryFile == "" {
		log.Fatalf("-registry flag is required (path to field_metadata.json)")
	}
	log.Printf("Loading field registry from: %s", registryFile)
	registry, err := markers.LoadRegistryFromJSON(registryFile)
	if err != nil {
		log.Fatalf("Failed to load registry: %v", err)
	}
	log.Printf("Loaded %d field markers from registry", len(registry))

	// Create generator
	var gen *passthrough.Generator

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

	if typeOverrides != "" {
		overrides := make(map[string]string)
		for _, pair := range strings.Split(typeOverrides, ",") {
			parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(parts) != 2 {
				log.Fatalf("Invalid type override format %q, expected from=to", pair)
			}
			overrides[parts[0]] = parts[1]
		}
		gen.TypeOverrides = overrides
		log.Printf("Type overrides: %v", overrides)
	}

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

package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/defaults"
)

func main() {
	var (
		sourceDir   string
		outputFile  string
		packageName string
		verbose     bool
	)

	flag.StringVar(&sourceDir, "source-dir", "", "Directory containing source Go files with +kubebuilder:default markers (required)")
	flag.StringVar(&outputFile, "output-file", "", "Output file path for generated defaults (required)")
	flag.StringVar(&packageName, "package", "public", "Package name for generated code")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.Parse()

	if sourceDir == "" || outputFile == "" {
		flag.Usage()
		os.Exit(1)
	}

	log.Printf("Scanning for default markers in: %s", sourceDir)

	gen := defaults.NewGenerator(sourceDir, packageName, verbose)

	// Scan for markers
	if err := gen.ScanDefaults(); err != nil {
		log.Fatalf("Failed to scan defaults: %v", err)
	}

	log.Printf("Found %d fields with default values", len(gen.Defaults))

	// Generate defaults file
	if err := gen.Generate(outputFile); err != nil {
		log.Fatalf("Failed to generate defaults: %v", err)
	}

	fmt.Printf("Successfully generated defaults in %s\n", outputFile)
}

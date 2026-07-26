package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/conversion"
)

func main() {
	var (
		apiVersion string
		crdPackage string
		inputDirs  string
		outputDir  string
	)

	flag.StringVar(&apiVersion, "api-version", "v1alpha1", "API version to generate for")
	flag.StringVar(&crdPackage, "crd-package", "", "Import path to CRD types (required)")
	flag.StringVar(&inputDirs, "input-dirs", "", "Comma-separated list of directories containing CRD source files (required)")
	flag.StringVar(&outputDir, "output-dir", "", "Output directory for generated code (required)")
	flag.Parse()

	if crdPackage == "" || inputDirs == "" || outputDir == "" {
		flag.Usage()
		fmt.Fprintf(os.Stderr, "\nError: Missing required flags\n\n")
		fmt.Fprintf(os.Stderr, "Example usage:\n")
		fmt.Fprintf(os.Stderr, "  %s \\\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "    --api-version=v1alpha1 \\\n")
		fmt.Fprintf(os.Stderr, "    --crd-package=github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/api/v1alpha1 \\\n")
		fmt.Fprintf(os.Stderr, "    --input-dirs=./api/v1alpha1 \\\n")
		fmt.Fprintf(os.Stderr, "    --output-dir=./pkg/conversion/v1alpha1\n")
		os.Exit(1)
	}

	// Split input directories
	dirs := strings.Split(inputDirs, ",")
	for i, dir := range dirs {
		// Convert to absolute path
		absDir, err := filepath.Abs(dir)
		if err != nil {
			log.Fatalf("Failed to resolve directory %s: %v", dir, err)
		}
		dirs[i] = absDir
	}

	// Create generator
	gen := conversion.NewGenerator(apiVersion, crdPackage, dirs, outputDir)

	log.Printf("Conversion code generator")
	log.Printf("  API Version: %s", apiVersion)
	log.Printf("  CRD Package: %s", crdPackage)
	log.Printf("  Input Dirs: %s", strings.Join(dirs, ", "))
	log.Printf("  Output Dir: %s", outputDir)
	log.Println()

	// Generate
	if err := gen.Generate(); err != nil {
		log.Fatalf("Generation failed: %v", err)
	}

	log.Println("✓ Successfully generated:")
	log.Println("  - REST types (rest/)")
	log.Println("  - ServiceSetFields (../types.go)")
	log.Println("  - Conversion functions (cluster.go, nodepool.go)")
}

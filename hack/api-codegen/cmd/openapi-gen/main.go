package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/openapi"
)

func main() {
	var (
		inputDirs  string
		outputFile string
		title      string
		version    string
	)

	flag.StringVar(&inputDirs, "input-dirs", "", "Comma-separated list of directories to scan for Go types (required)")
	flag.StringVar(&outputFile, "output-file", "", "Output file for OpenAPI schema (required)")
	flag.StringVar(&title, "title", "HyperFleet API", "API title")
	flag.StringVar(&version, "version", "v1alpha1", "API version")
	flag.Parse()

	if outputFile == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Parse input directories
	var dirs []string
	if inputDirs != "" {
		for _, dir := range strings.Split(inputDirs, ",") {
			dirs = append(dirs, strings.TrimSpace(dir))
		}
	}

	// Create generator
	gen := openapi.NewGenerator(dirs, outputFile)
	gen.Title = title
	gen.Version = version

	log.Printf("Generating OpenAPI schema: %s v%s", title, version)
	if len(dirs) > 0 {
		log.Printf("Scanning directories: %v", dirs)
	} else {
		log.Println("No input directories specified - generating minimal POC schema")
	}

	if err := gen.Generate(); err != nil {
		log.Fatalf("Failed to generate OpenAPI schema: %v", err)
	}

	fmt.Printf("Successfully generated OpenAPI schema at %s\n", outputFile)
	if len(dirs) > 0 {
		fmt.Println("Schema includes all types with visible fields (+k8s:openapi-gen=false fields excluded)")
	}
}

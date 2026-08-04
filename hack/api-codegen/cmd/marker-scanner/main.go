package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/markers"
)

func main() {
	var (
		inputDirs  string
		outputFile string
		validate   bool
		verbose    bool
	)

	flag.StringVar(&inputDirs, "input-dirs", "", "Comma-separated list of directories to scan (required)")
	flag.StringVar(&outputFile, "output-file", "", "Output file for generated registry (required)")
	flag.BoolVar(&validate, "validate", true, "Validate that all visible fields have write-mode markers")
	flag.BoolVar(&verbose, "verbose", false, "Show detailed table of fields and their markers")
	flag.Parse()

	if inputDirs == "" || outputFile == "" {
		flag.Usage()
		os.Exit(1)
	}

	dirs := strings.Split(inputDirs, ",")
	for i := range dirs {
		dirs[i] = strings.TrimSpace(dirs[i])
	}

	// Create scanner and scan directories
	scanner := markers.NewScanner(dirs, verbose)

	log.Printf("Scanning directories: %v", dirs)
	if err := scanner.Scan(); err != nil {
		log.Fatalf("Error scanning: %v", err)
	}

	log.Printf("Found %d fields with markers", len(scanner.Registry))

	// Show scanned fields if verbose
	if verbose {
		fmt.Println()
		fmt.Println("=== Scanned Fields ===")
		fmt.Println()
		printRegistryTable(scanner.Registry)
		printRegistryStats(scanner.Registry)
		fmt.Println()
	}

	// Validate if requested
	if validate {
		if err := scanner.Registry.Validate(); err != nil {
			log.Fatalf("Validation failed: %v", err)
		}
		log.Println("Validation passed")
	}

	// Generate registry file
	log.Printf("Generating registry: %s", outputFile)
	if err := scanner.Generate(outputFile); err != nil {
		log.Fatalf("Error generating registry: %v", err)
	}

	fmt.Printf("Successfully generated field registry at %s\n", outputFile)

	// Also generate JSON file for use by other tools
	jsonFile := strings.TrimSuffix(outputFile, ".go") + ".json"
	log.Printf("Generating JSON registry: %s", jsonFile)
	if err := scanner.GenerateJSON(jsonFile); err != nil {
		log.Fatalf("Error generating JSON registry: %v", err)
	}

	fmt.Printf("Successfully generated JSON registry at %s\n", jsonFile)

	// Show what was generated if verbose
	if verbose {
		fmt.Println()
		fmt.Println("=== Generated Registry Contents ===")
		fmt.Printf("File: %s\n", outputFile)
		fmt.Printf("Package: registry\n")
		fmt.Printf("Exported: FieldRegistry map[string]FieldMeta\n")
		fmt.Println()
		fmt.Println("The generated file contains:")
		printRegistryTable(scanner.Registry)
		printRegistryStats(scanner.Registry)
	}
}

// printRegistryTable displays the field registry as a formatted table
func printRegistryTable(registry markers.FieldRegistry) {
	// Sort field paths
	var paths []string
	for path := range registry {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	// Create table writer
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "FIELD PATH\tWRITE MODE\tFEATURE GATE\tHIDDEN")
	_, _ = fmt.Fprintln(w, "----------\t----------\t------------\t------")

	// Print each field
	for _, path := range paths {
		meta := registry[path]

		writeMode := string(meta.WriteMode)
		if writeMode == "" {
			writeMode = "-"
		}

		featureGate := meta.FeatureGate
		if featureGate == "" {
			featureGate = "-"
		}

		hidden := "no"
		if meta.Hidden {
			hidden = "yes"
		}

		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", path, writeMode, featureGate, hidden)
	}

	_ = w.Flush()
}

// printRegistryStats displays summary statistics about the registry
func printRegistryStats(registry markers.FieldRegistry) {
	var (
		mutable    int
		immutable  int
		serviceSet int
		hidden     int
		gated      int
	)

	for _, meta := range registry {
		switch meta.WriteMode {
		case markers.Mutable:
			mutable++
		case markers.Immutable:
			immutable++
		case markers.ServiceSet:
			serviceSet++
		}
		if meta.Hidden {
			hidden++
		}
		if meta.FeatureGate != "" {
			gated++
		}
	}

	fmt.Println()
	fmt.Printf("Summary: %d total fields\n", len(registry))
	fmt.Printf("  Write Modes: %d mutable, %d immutable, %d service-set\n", mutable, immutable, serviceSet)
	fmt.Printf("  Visibility:  %d visible, %d hidden\n", len(registry)-hidden, hidden)
	fmt.Printf("  Gating:      %d gated, %d ungated\n", gated, len(registry)-gated)
}

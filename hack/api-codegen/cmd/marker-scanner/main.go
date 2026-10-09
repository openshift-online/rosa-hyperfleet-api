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
	scanner, err := markers.NewScanner(dirs, verbose)
	if err != nil {
		panic(fmt.Sprintf("Error creating scanner: %v", err))
	}

	log.Printf("Scanning directories: %v", dirs)
	if err := scanner.Scan(); err != nil {
		log.Fatalf("Error scanning: %v", err)
	}

	// Count total fields across all owner types
	totalFields := 0
	for _, fields := range scanner.TypedRegistry {
		totalFields += len(fields)
	}
	log.Printf("Found %d fields with markers across %d CRD types", totalFields, len(scanner.TypedRegistry))

	// Show scanned fields if verbose
	if verbose {
		if _, err := fmt.Println(); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if _, err := fmt.Println("=== Scanned Fields ==="); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if _, err := fmt.Println(); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if err := printTypedRegistryTable(scanner.TypedRegistry); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if err := printTypedRegistryStats(scanner.TypedRegistry); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if _, err := fmt.Println(); err != nil {
			log.Fatalf("Output error: %v", err)
		}
	}

	// Validate if requested
	if validate {
		if err := scanner.TypedRegistry.Validate(); err != nil {
			log.Fatalf("Validation failed: %v", err)
		}
		log.Println("Validation passed")
	}

	scanner.UpdateActions, err = markers.DefaultUpdateActions()
	if err != nil {
		log.Fatalf("Error loading update actions: %v", err)
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
		if _, err := fmt.Println(); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if _, err := fmt.Println("=== Generated Registry Contents ==="); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if _, err := fmt.Printf("File: %s\n", outputFile); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if _, err := fmt.Printf("Package: registry\n"); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if _, err := fmt.Printf("Exported: FieldRegistry TypedFieldRegistry (map[string]map[string]FieldMeta)\n"); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if _, err := fmt.Println(); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if _, err := fmt.Println("The generated file contains:"); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if err := printTypedRegistryTable(scanner.TypedRegistry); err != nil {
			log.Fatalf("Output error: %v", err)
		}
		if err := printTypedRegistryStats(scanner.TypedRegistry); err != nil {
			log.Fatalf("Output error: %v", err)
		}
	}
}

// printTypedRegistryTable displays the typed field registry as a formatted table
func printTypedRegistryTable(registry markers.TypedFieldRegistry) error {
	// Sort owner types
	var owners []string
	for owner := range registry {
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	// Create table writer
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "OWNER TYPE\tFIELD PATH\tWRITE MODE\tFEATURE GATE\tGATED WRITE MODES\tHIDDEN"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "-----------\t-----------\t-----------\t-----------\t-----------\t------"); err != nil {
		return err
	}

	// Print each owner type and its fields
	for _, owner := range owners {
		fields := registry[owner]

		// Sort field paths
		var paths []string
		for path := range fields {
			paths = append(paths, path)
		}
		sort.Strings(paths)

		// Print each field
		for _, path := range paths {
			meta := fields[path]

			writeMode := string(meta.WriteMode)
			if writeMode == "" {
				writeMode = "-"
			}

			featureGate := meta.FeatureGate
			if featureGate == "" {
				featureGate = "-"
			}

			gatedWriteModes := "-"
			if len(meta.FeatureGateAwareWriteModes) > 0 {
				var modes []string
				for _, gated := range meta.FeatureGateAwareWriteModes {
					modes = append(modes, fmt.Sprintf("%s:%s", gated.FeatureGate, gated.WriteMode))
				}
				gatedWriteModes = strings.Join(modes, ";")
			}

			hidden := "no"
			if meta.HiddenFromPublicAPI() {
				hidden = "yes"
			}

			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", owner, path, writeMode, featureGate, gatedWriteModes, hidden); err != nil {
				return err
			}
		}
	}

	if err := w.Flush(); err != nil {
		return err
	}
	return nil
}

// printTypedRegistryStats displays summary statistics about the typed registry
func printTypedRegistryStats(registry markers.TypedFieldRegistry) error {
	var (
		totalFields int
		mutable     int
		immutable   int
		serviceSet  int
		hidden      int
		gated       int
	)

	for _, fields := range registry {
		for _, meta := range fields {
			totalFields++
			switch meta.WriteMode {
			case markers.Mutable:
				mutable++
			case markers.Immutable:
				immutable++
			case markers.ServiceSet:
				serviceSet++
			}
			if meta.HiddenFromPublicAPI() {
				hidden++
			}
			if meta.FeatureGate != "" || len(meta.FeatureGateAwareWriteModes) > 0 {
				gated++
			}
		}
	}

	if _, err := fmt.Println(); err != nil {
		return err
	}
	if _, err := fmt.Printf("Summary: %d total fields across %d CRD types\n", totalFields, len(registry)); err != nil {
		return err
	}
	if _, err := fmt.Printf("  Write Modes: %d mutable, %d immutable, %d service-set\n", mutable, immutable, serviceSet); err != nil {
		return err
	}
	if _, err := fmt.Printf("  Visibility:  %d visible, %d hidden\n", totalFields-hidden, hidden); err != nil {
		return err
	}
	if _, err := fmt.Printf("  Gating:      %d gated, %d ungated\n", gated, totalFields-gated); err != nil {
		return err
	}
	return nil
}

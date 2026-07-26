package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/featuregate"
)

func main() {
	var (
		inputFile  = flag.String("input", "", "Input CRD YAML file")
		outputDir  = flag.String("output-dir", "config/crd/variants", "Output directory for CRD variants")
		baseName   = flag.String("base-name", "", "Base name for output files (e.g., 'cluster' produces cluster_default.yaml)")
		featureSet = flag.String("feature-set", "", "Generate only one feature set variant (default, techpreview, devpreview)")
	)

	flag.Parse()

	if *inputFile == "" {
		fmt.Fprintln(os.Stderr, "Error: --input is required")
		flag.Usage()
		os.Exit(1)
	}

	if *baseName == "" {
		fmt.Fprintln(os.Stderr, "Error: --base-name is required")
		flag.Usage()
		os.Exit(1)
	}

	// Create output directory
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Creating output directory: %v", err)
	}

	g := featuregate.NewCRDVariantGenerator()

	if *featureSet != "" {
		// Generate single variant
		var fs featuregate.FeatureSet
		switch *featureSet {
		case "default":
			fs = featuregate.Default
		case "techpreview":
			fs = featuregate.TechPreviewNoUpgrade
		case "devpreview":
			fs = featuregate.DevPreviewNoUpgrade
		default:
			log.Fatalf("Invalid feature set: %s (must be default, techpreview, or devpreview)", *featureSet)
		}

		outputPath := fmt.Sprintf("%s/%s_%s.yaml", *outputDir, *baseName, *featureSet)
		fmt.Printf("Generating %s variant: %s\n", *featureSet, outputPath)

		if err := g.GenerateVariant(*inputFile, outputPath, fs); err != nil {
			log.Fatalf("Generating variant: %v", err)
		}

		fmt.Printf("✓ Generated %s variant\n", *featureSet)
	} else {
		// Generate all variants
		fmt.Printf("Generating all CRD variants from %s to %s/\n", *inputFile, *outputDir)

		if err := g.GenerateAllVariants(*inputFile, *outputDir, *baseName); err != nil {
			log.Fatalf("Generating variants: %v", err)
		}

		fmt.Printf("✓ Generated default variant: %s/%s_default.yaml\n", *outputDir, *baseName)
		fmt.Printf("✓ Generated techpreview variant: %s/%s_techpreview.yaml\n", *outputDir, *baseName)
		fmt.Printf("✓ Generated devpreview variant: %s/%s_devpreview.yaml\n", *outputDir, *baseName)
	}
}

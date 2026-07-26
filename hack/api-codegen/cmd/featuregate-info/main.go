package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/featuregate"
)

func main() {
	fmt.Println("=== HyperFleet Feature Gate Registry ===")
	fmt.Println()

	// List all feature gates
	fmt.Println("Registered Feature Gates:")
	fmt.Println()

	gates := make([]string, 0, len(featuregate.HyperFleetFeatureGates))
	for gate := range featuregate.HyperFleetFeatureGates {
		gates = append(gates, gate)
	}
	sort.Strings(gates)

	for _, gate := range gates {
		info := featuregate.HyperFleetFeatureGates[gate]
		fmt.Printf("  %-30s  Stage: %-12s  %s\n", gate, info.Stage, info.Description)
	}

	fmt.Println()
	fmt.Println("=== Feature Set Field Summary ===")
	fmt.Println()

	featureSets := []featuregate.FeatureSet{
		featuregate.Default,
		featuregate.TechPreviewNoUpgrade,
		featuregate.DevPreviewNoUpgrade,
	}

	for _, fs := range featureSets {
		fields := featuregate.FieldsForFeatureSet(fs)
		gates := featuregate.GatesForFeatureSet(fs)

		fmt.Printf("%s:\n", fs)
		fmt.Printf("  Total visible fields: %d\n", len(fields))
		fmt.Printf("  Enabled gates: %v\n", gates)
		fmt.Println()
	}

	os.Exit(0)
}

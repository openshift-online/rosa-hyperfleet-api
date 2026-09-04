// pathbind-gen generates consumer-facing code from pathbind configuration files.
//
// Modes:
//
//	--mode=init   reads field_metadata.json and writes pathbind-draft.yaml.
//	              Run by the SDK's make generate-pathbind-draft target.
//
//	--mode=cobra  reads pathbind-draft.yaml + pathbind-overrides.yaml and writes
//	              Go source (input struct, RegisterXxxFlags, XxxHandler interface,
//	              GeneratedXxxPrompt, RunXxx) into the consumer's package.
//	              Run by the consumer's make generate-hyperfleet target.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	mode := flag.String("mode", "", "generation mode: init or cobra (required)")

	// init mode
	registryPath := flag.String("registry", "", "[init] path to field_metadata.json")
	openapiPath := flag.String("openapi", "", "[init] path to openapi.yaml for leaf-path resolution (optional)")
	draftOutput := flag.String("output", "", "[init] path to write pathbind-draft.yaml")

	// cobra mode
	draftPath := flag.String("draft", "", "[cobra] path to pathbind-draft.yaml (optional)")
	overridesPath := flag.String("overrides", "", "[cobra] path to pathbind-overrides.yaml")
	outputDir := flag.String("output-dir", "", "[cobra] directory to write generated Go files")

	flag.Parse()

	if *mode == "" {
		fatalf("--mode is required; use init or cobra")
	}

	switch *mode {
	case "init":
		if *registryPath == "" || *draftOutput == "" {
			fatalf("--registry and --output are required for --mode=init")
		}
		if err := runInit(*registryPath, *openapiPath, *draftOutput); err != nil {
			log.Fatalf("pathbind-gen: %v", err)
		}

	case "cobra":
		if *overridesPath == "" || *outputDir == "" {
			fatalf("--overrides and --output-dir are required for --mode=cobra")
		}
		if err := runCobra(*draftPath, *overridesPath, *outputDir); err != nil {
			log.Fatalf("pathbind-gen: %v", err)
		}

	default:
		fatalf("unknown mode %q; use init or cobra", *mode)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "pathbind-gen: "+format+"\n", args...)
	os.Exit(1)
}

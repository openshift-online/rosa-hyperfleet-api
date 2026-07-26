package passthrough

import (
	"fmt"
	"go/ast"
	"os/exec"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/markers"
)

// ResolvePackageDir uses `go list` to find the directory for a Go package import path.
// This works for both local packages and module dependencies in go.mod.
//
// Example:
//
//	ResolvePackageDir("github.com/openshift/hypershift/api/hypershift/v1beta1")
//	=> "/Users/user/go/pkg/mod/github.com/openshift/hypershift/api@v0.0.0-20251113065312-f919037748bf/hypershift/v1beta1"
func ResolvePackageDir(importPath string) (string, error) {
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", importPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to resolve package %s: %w\nOutput: %s", importPath, err, string(output))
	}

	dir := strings.TrimSpace(string(output))
	if dir == "" {
		return "", fmt.Errorf("go list returned empty directory for %s", importPath)
	}

	return dir, nil
}

// NewGeneratorFromImportPath creates a generator by resolving the source directory
// from a Go import path using `go list`.
//
// Example:
//
//	gen, err := NewGeneratorFromImportPath(
//	    "github.com/openshift/hypershift/api/hypershift/v1beta1",
//	    []string{"HostedClusterSpec", "NodePoolSpec"},
//	    registry,
//	)
func NewGeneratorFromImportPath(importPath string, sourceTypes []string, registry markers.FieldRegistry) (*Generator, error) {
	sourceDir, err := ResolvePackageDir(importPath)
	if err != nil {
		return nil, fmt.Errorf("resolving import path %s: %w", importPath, err)
	}

	// Extract package alias from import path (last segment)
	parts := strings.Split(importPath, "/")
	packageAlias := "hypershift" + parts[len(parts)-1] // e.g., "hypershiftv1beta1"

	return &Generator{
		SourceDir:          sourceDir,
		SourceTypes:        sourceTypes,
		OutputPackage:      "v1alpha1",
		Registry:           registry,
		SourcePackage:      importPath,
		SourcePackageAlias: packageAlias,
		parsedFiles:        make(map[string]*ast.File),
	}, nil
}

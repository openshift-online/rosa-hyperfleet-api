/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// bridge-gen reads +bridge:* markers from Go source files and emits
// bridge_wrappers_generated.go containing wrapper types for resources
// annotated with +bridge:watch=disabled and/or +bridge:wait.
//
// The Watch method returns ErrWatchNotSupported; WaitUntil provides
// polling-based synchronization via a caller-supplied condition function.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"
)

//go:embed templates/platform.go.tmpl
var platformTmpl string

var (
	watchDisabledRE = regexp.MustCompile(`\+bridge:watch=disabled`)
	waitRE          = regexp.MustCompile(`\+bridge:wait\b`)
	nonNamespacedRE = regexp.MustCompile(`\+genclient:nonNamespaced\b`)
)

// resourceType describes a CRD type annotated with +bridge:watch or +bridge:wait.
type resourceType struct {
	Name          string // e.g. "Cluster"
	PluralName    string // e.g. "Clusters"
	PluralLower   string // e.g. "clusters" — URL path segment key
	LowerName     string // e.g. "cluster"
	WatchDisabled bool
	Wait          bool
	NonNamespaced bool // set when +genclient:nonNamespaced is present
}

// ── main ─────────────────────────────────────────────────────────────────────

func main() {
	inputDir := flag.String("input-dir", "", "directory of Go source files to scan for +bridge:* markers")
	outputDir := flag.String("output-dir", "", "directory to write the generated file")
	outputPkg := flag.String("output-pkg", "platform", "Go package name for the generated file")
	headerFile := flag.String("go-header-file", "", "file whose contents are prepended to the generated output")
	typedPkg := flag.String("typed-pkg-import", "", "import path of the generated typed client package")
	apiPkg := flag.String("api-pkg-import", "", "import path of the CRD API types package")
	typedClientPrefix := flag.String("typed-client-prefix", "V1alpha1", "group-level interface name prefix (e.g. V1alpha1 or V1alpha1Public)")
	flag.Parse()

	if *inputDir == "" || *outputDir == "" {
		fmt.Fprintln(os.Stderr, "bridge-gen: --input-dir and --output-dir are required")
		os.Exit(1)
	}
	if *typedPkg == "" || *apiPkg == "" {
		fmt.Fprintln(os.Stderr, "bridge-gen: --typed-pkg-import and --api-pkg-import are required")
		os.Exit(1)
	}

	header := readHeader(*headerFile)

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		fatalf("creating output dir: %v", err)
	}

	types := collectResourceTypes(*inputDir)
	if len(types) == 0 {
		fatalf("no resource types with +bridge:watch or +bridge:wait markers found in %s", *inputDir)
	}

	outPath := filepath.Join(*outputDir, "bridge_wrappers_generated.go")
	f, err := os.Create(outPath)
	if err != nil {
		fatalf("creating %s: %v", outPath, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			fatalf("closing %s: %v", outPath, err)
		}
		fmt.Printf("bridge-gen: wrote %s\n", outPath)
	}()

	if _, err := fmt.Fprint(f, header); err != nil {
		fatalf("writing header to %s: %v", outPath, err)
	}

	anyWait, anyWatchDisabled := false, false
	for _, t := range types {
		if t.Wait {
			anyWait = true
		}
		if t.WatchDisabled {
			anyWatchDisabled = true
		}
	}

	var buf strings.Builder
	tmpl := template.Must(template.New("platform").Parse(platformTmpl))
	if err := tmpl.Execute(&buf, map[string]any{
		"Package":           *outputPkg,
		"ApiPkgImport":      *apiPkg,
		"TypedPkgImport":    *typedPkg,
		"TypedClientPrefix": *typedClientPrefix,
		"Types":             types,
		"AnyWait":           anyWait,
		"AnyWatchDisabled":  anyWatchDisabled,
	}); err != nil {
		fatalf("rendering template: %v", err)
	}
	formatted, fmtErr := format.Source([]byte(buf.String()))
	if fmtErr != nil {
		fatalf("gofmt: %v", fmtErr)
	}
	if _, err := f.Write(formatted); err != nil {
		fatalf("writing formatted output: %v", err)
	}
}

// collectResourceTypes scans all Go source files in dir for type declarations
// annotated with +bridge:watch=disabled and/or +bridge:wait.
//
// Because generators conventionally place markers in a floating comment block
// (separated by a blank line from the actual doc comment), markers are found by
// scanning all comment groups and associating each group with the nearest type
// declaration that follows it in the file.
func collectResourceTypes(dir string) []resourceType {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ParseComments)
	if err != nil {
		fatalf("parsing %s: %v", dir, err)
	}

	seen := map[string]bool{}
	var types []resourceType

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			type typePos struct {
				name string
				pos  token.Pos
			}
			var typeDecls []typePos
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					typeDecls = append(typeDecls, typePos{name: ts.Name.Name, pos: gd.Pos()})
				}
			}
			sort.Slice(typeDecls, func(i, j int) bool { return typeDecls[i].pos < typeDecls[j].pos })

			for _, cg := range file.Comments {
				var hasWatch, hasWait, hasNonNamespaced bool
				for _, c := range cg.List {
					if watchDisabledRE.MatchString(c.Text) {
						hasWatch = true
					}
					if waitRE.MatchString(c.Text) {
						hasWait = true
					}
					if nonNamespacedRE.MatchString(c.Text) {
						hasNonNamespaced = true
					}
				}
				if !hasWatch && !hasWait {
					continue
				}

				cgEnd := cg.End()
				for _, td := range typeDecls {
					if td.pos <= cgEnd || seen[td.name] {
						continue
					}
					seen[td.name] = true
					name := td.name
					plural := name + "s"
					types = append(types, resourceType{
						Name:          name,
						PluralName:    plural,
						PluralLower:   strings.ToLower(plural),
						LowerName:     strings.ToLower(name[:1]) + name[1:],
						WatchDisabled: hasWatch,
						Wait:          hasWait,
						NonNamespaced: hasNonNamespaced,
					})
					break
				}
			}
		}
	}

	sort.Slice(types, func(i, j int) bool { return types[i].Name < types[j].Name })
	return types
}

// ── helpers ───────────────────────────────────────────────────────────────────

func readHeader(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fatalf("reading header file: %v", err)
	}
	return strings.ReplaceAll(string(b), "YEAR", strconv.Itoa(time.Now().Year()))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bridge-gen: "+format+"\n", args...)
	os.Exit(1)
}

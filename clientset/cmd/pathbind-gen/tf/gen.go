package tf

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	pkg "github.com/openshift-online/rosa-hyperfleet-api/clientset/cmd/pathbind-gen/pkg"
	"gopkg.in/yaml.v3"
)

// Run orchestrates the TF code generation from pathbind configuration.
func Run(draftPath, overridesPath, outputDir string) error {
	rawOv, err := loadOverrides(overridesPath)
	if err != nil {
		return err
	}

	cfg := rawOv.Config
	if cfg.Package == "" || cfg.TFProviderPkg == "" {
		return fmt.Errorf("overrides config must set package and tfProviderPkg for --mode=tf")
	}

	draftIndex := map[string]map[string]pkg.DraftField{}
	draftSDKTypes := map[string]string{}
	if err := pkg.LoadDraft(draftPath, draftIndex, draftSDKTypes); err != nil {
		return err
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	funcMap := buildFuncMap()

	stateTmpl := template.Must(template.New("state").Funcs(funcMap).Parse(stateTemplate))
	stateNativeTmpl := template.Must(template.New("stateNative").Funcs(funcMap).Parse(stateNativeTemplate))
	resourceTmpl := template.Must(template.New("resource").Funcs(funcMap).Parse(resourceTemplate))
	utilsGenTmpl := template.Must(template.New("utilsGen").Funcs(funcMap).Parse(utilsGenTemplate))

	allResKeys := pkg.UnionKeys(draftSDKTypes, rawOv.Resources)
	for _, resKey := range allResKeys {
		ovRes := rawOv.Resources[resKey]

		sdkType, ok := draftSDKTypes[resKey]
		if !ok {
			sdkType = pkg.SDKTypeForOwner[pkg.TitleCase(resKey)]
		}

		// Merge draft fields with override configuration
		aliases := pkg.BuildMergedAliases(draftIndex[resKey], ovRes.Aliases)

		// Categorize fields into create/update
		createFields, _, _, updateFields, _, _ := pkg.CategorizeAliases(aliases)

		resName := pkg.TitleCase(resKey)
		sdkShort := pkg.SDKShortType(sdkType)

		// Collect immutable and computed fields for schema generation
		immutableList := []string{}
		computedList := []string{}
		for _, a := range aliases {
			if a.Immutable {
				immutableList = append(immutableList, a.GoName)
			}
			if a.Computed {
				computedList = append(computedList, a.GoName)
			}
		}

		td := pkg.TFTemplateData{
			Package:        cfg.Package,
			ResourceName:   resName,
			SDKType:        sdkType,
			SDKShortType:   sdkShort,
			AllFields:      aliases,
			CreateFields:   createFields,
			UpdateFields:   updateFields,
			ImmutableList:  immutableList,
			ComputedList:   computedList,
			Namespaced:     pkg.IsNamespacedResource(resKey),
			HandlerFactory: "New" + resName + "HandlerImpl",
		}

		// Generate state struct file: <resource>_state_gen.go
		if err := emitFile(stateTmpl, td, filepath.Join(outputDir, strings.ToLower(resName)+"_state_gen.go")); err != nil {
			return err
		}

		// Generate native state struct file: <resource>_state_native_gen.go
		if err := emitFile(stateNativeTmpl, td, filepath.Join(outputDir, strings.ToLower(resName)+"_state_native_gen.go")); err != nil {
			return err
		}

		// Generate resource base + handler interface + CRUD: <resource>_resource_gen.go
		if err := emitFile(resourceTmpl, td, filepath.Join(outputDir, strings.ToLower(resName)+"_resource_gen.go")); err != nil {
			return err
		}
	}

	// Generate shared utils file (only once, not per resource)
	// Use minimal template data since utils doesn't need resource-specific info
	utilsData := pkg.TFTemplateData{
		Package: cfg.Package,
	}
	if err := emitFile(utilsGenTmpl, utilsData, filepath.Join(outputDir, "utils_gen.go")); err != nil {
		return err
	}

	return nil
}

func loadOverrides(path string) (*pkg.Overrides, error) {
	rawOv, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading overrides %s: %w", path, err)
	}
	var ov pkg.Overrides
	if err := yaml.Unmarshal(rawOv, &ov); err != nil {
		return nil, fmt.Errorf("parsing overrides: %w", err)
	}
	return &ov, nil
}

func emitFile(tmpl *template.Template, data pkg.TFTemplateData, path string) error {
	var buf bytes.Buffer

	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("rendering %s: %w", path, err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		debugPath := path + ".debug"
		_ = os.WriteFile(debugPath, buf.Bytes(), 0o644)
		return fmt.Errorf("formatting %s: %w\n(unformatted written to %s)", path, err, debugPath)
	}

	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Printf("pathbind-gen: wrote %s\n", path)
	return nil
}

func buildFuncMap() template.FuncMap {
	return template.FuncMap{
		"lower": strings.ToLower,
		"hasSuffix": strings.HasSuffix,
		"contains": func(slice []string, item string) bool {
			for _, o := range slice {
				if o == item {
					return true
				}
			}
			return false
		},
		// tfType maps Go type to Terraform framework attr types
		"tfType": func(a pkg.MergedAlias) string {
			switch a.Type {
			case "string", "*string":
				return "types.StringType"
			case "bool", "*bool":
				return "types.BoolType"
			case "int32", "*int32", "int64", "*int64":
				return "types.Int64Type"
			default:
				return "types.StringType"
			}
		},
		// tfTypeValue returns the TF framework value constructor
		"tfTypeValue": func(a pkg.MergedAlias) string {
			switch a.Type {
			case "string", "*string":
				return "types.StringValue"
			case "bool", "*bool":
				return "types.BoolValue"
			case "int32", "*int32", "int64", "*int64":
				return "types.Int64Value"
			default:
				return "types.StringValue"
			}
		},
		// attrName converts GoName to snake_case attribute name (Terraform requirement)
		"attrName": func(a pkg.MergedAlias) string {
			return pkg.ToSnake(a.GoName)
		},
		// planModifiers generates plan modifiers based on field attributes
		"planModifiers": func(a pkg.MergedAlias) string {
			var modifiers []string
			if a.Immutable {
				modifiers = append(modifiers, "stringplanmodifier.RequiresReplace()")
			}
			if a.Computed {
				modifiers = append(modifiers, "stringplanmodifier.UseStateForUnknown()")
			}
			if a.Sensitive {
				modifiers = append(modifiers, "stringplanmodifier.Sensitive()")
			}
			return strings.Join(modifiers, ", ")
		},
		// isConsumerOnly checks if field is hidden from Terraform schema.
		// Fields with hfsdk:"-" but Operations defined are Terraform inputs (not consumer-only).
		// Only truly hidden fields have hfsdk:"-" AND no Operations.
		"isConsumerOnly": func(a pkg.MergedAlias) bool {
			return a.Path == "-" && len(a.Operations) == 0
		},
		// hfsdkTag returns the hfsdk tag value (path or "-" for consumer-only)
		"hfsdkTag": func(a pkg.MergedAlias) string {
			if a.Path == "" || a.Path == "-" {
				return "-"
			}
			return a.Path
		},
		// pluralize converts singular to plural (Cluster → Clusters)
		"pluralize": func(s string) string {
			if strings.HasSuffix(s, "s") {
				return s + "es"
			}
			return s + "s"
		},
		// sdkImportPath extracts the import path from full SDK type (v1alpha1.Cluster → v1alpha1)
		"sdkPackage": func(sdkType string) string {
			parts := strings.Split(sdkType, ".")
			if len(parts) > 1 {
				return parts[0]
			}
			return sdkType
		},
		// schemaType determines the Terraform schema attribute type based on Go type
		"schemaType": func(a pkg.MergedAlias) string {
			// Check if it's a list/array type
			if strings.Contains(a.Type, "[]") || strings.Contains(a.Type, "types.List") {
				return "List"
			}
			// Check if it's a map type
			if a.Type == "map" || strings.Contains(a.Type, "map[") || strings.Contains(a.Type, "types.Map") {
				return "Map"
			}
			// Check if it's a bool type
			if strings.Contains(a.Type, "bool") || strings.Contains(a.Type, "types.Bool") {
				return "Bool"
			}
			// Check if it's an int type
			if strings.Contains(a.Type, "int") || strings.Contains(a.Type, "types.Int") {
				return "Int64"
			}
			// Default to String
			return "String"
		},
	}
}

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

type terraformTypeMapping struct {
	FrameworkType string
	ValueFunction string
	SchemaType    string
}

var terraformTypeMappings = map[string]terraformTypeMapping{
	"string":       {FrameworkType: "types.StringType", ValueFunction: "types.StringValue", SchemaType: "String"},
	"*string":      {FrameworkType: "types.StringType", ValueFunction: "types.StringValue", SchemaType: "String"},
	"bool":         {FrameworkType: "types.BoolType", ValueFunction: "types.BoolValue", SchemaType: "Bool"},
	"*bool":        {FrameworkType: "types.BoolType", ValueFunction: "types.BoolValue", SchemaType: "Bool"},
	"int32":        {FrameworkType: "types.Int64Type", ValueFunction: "types.Int64Value", SchemaType: "Int64"},
	"*int32":       {FrameworkType: "types.Int64Type", ValueFunction: "types.Int64Value", SchemaType: "Int64"},
	"int64":        {FrameworkType: "types.Int64Type", ValueFunction: "types.Int64Value", SchemaType: "Int64"},
	"*int64":       {FrameworkType: "types.Int64Type", ValueFunction: "types.Int64Value", SchemaType: "Int64"},
	"string[]":     {FrameworkType: "types.ListType", ValueFunction: "types.ListValue", SchemaType: "List"},
	"list(object)": {FrameworkType: "types.ListType", ValueFunction: "types.ListValue", SchemaType: "ListNested"},
	"map":          {FrameworkType: "types.MapType", ValueFunction: "types.MapValue", SchemaType: "Map"},
}

func terraformTypeFor(typ string) (terraformTypeMapping, error) {
	mapping, ok := terraformTypeMappings[typ]
	if !ok {
		return terraformTypeMapping{}, fmt.Errorf("unsupported Terraform consumer type %q", typ)
	}
	return mapping, nil
}

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
		aliases, err := pkg.BuildMergedAliases(draftIndex[resKey], ovRes.Aliases)
		if err != nil {
			return fmt.Errorf("building aliases for %s: %w", resKey, err)
		}
		for _, alias := range aliases {
			if _, err := terraformTypeFor(alias.Type); err != nil {
				return fmt.Errorf("building aliases for %s: field %s: %w", resKey, alias.Path, err)
			}
		}

		// Categorize fields into create/update
		createFields, _, _, updateFields, _, _ := pkg.CategorizeAliases(aliases)
		topFields, bundles, err := buildBundles(aliases)
		if err != nil {
			return fmt.Errorf("building bundles for %s: %w", resKey, err)
		}

		resName := pkg.TitleCase(resKey)
		sdkShort := pkg.SDKShortType(sdkType)

		// Collect immutable and computed fields for schema generation
		immutableList := []string{}
		computedList := []string{}
		identifierField := ""
		for _, a := range aliases {
			if a.Immutable {
				immutableList = append(immutableList, a.GoName)
			}
			if a.Computed {
				computedList = append(computedList, a.GoName)
			}
			if a.Path == "metadata.uid" {
				identifierField = a.GoName
			}
		}
		if identifierField == "" {
			return fmt.Errorf("building aliases for %s: metadata.uid field is missing", resKey)
		}

		td := pkg.TFTemplateData{
			Package:         cfg.Package,
			ResourceName:    resName,
			SDKType:         sdkType,
			SDKShortType:    sdkShort,
			AllFields:       aliases,
			TopFields:       topFields,
			Bundles:         bundles,
			CreateFields:    createFields,
			UpdateFields:    updateFields,
			ImmutableList:   immutableList,
			ComputedList:    computedList,
			IdentifierField: identifierField,
			HandlerFactory:  "New" + resName + "HandlerImpl",
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

func buildBundles(aliases []pkg.MergedAlias) ([]pkg.MergedAlias, []pkg.AliasBundle, error) {
	var top []pkg.MergedAlias
	byName := map[string]*pkg.AliasBundle{}
	var order []string
	for _, alias := range aliases {
		if alias.Bundle == "" {
			top = append(top, alias)
			continue
		}
		if alias.Path == "metadata.uid" {
			return nil, nil, fmt.Errorf("field metadata.uid cannot be placed in bundle %q: Terraform resource identity must remain top-level", alias.Bundle)
		}
		name := pkg.ToPascal(alias.Bundle)
		if name == "" {
			return nil, nil, fmt.Errorf("field %s has an empty bundle name", alias.Path)
		}
		bundle := byName[name]
		if bundle == nil {
			bundle = &pkg.AliasBundle{Name: alias.Bundle, GoName: name}
			byName[name] = bundle
			order = append(order, name)
		}
		bundle.Fields = append(bundle.Fields, alias)
		if alias.Required {
			bundle.Required = true
		}
		if !alias.Required {
			bundle.Optional = true
		}
		if alias.Computed {
			bundle.Computed = true
		}
	}
	bundles := make([]pkg.AliasBundle, 0, len(order))
	for _, name := range order {
		bundles = append(bundles, *byName[name])
	}
	return top, bundles, nil
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
		if debugErr := os.WriteFile(debugPath, buf.Bytes(), 0o644); debugErr != nil {
			return fmt.Errorf("formatting %s: %w (also failed to write debug file %s: %v)", path, err, debugPath, debugErr)
		}
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
		"lower":     strings.ToLower,
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
		"tfType": func(a pkg.MergedAlias) (string, error) {
			mapping, err := terraformTypeFor(a.Type)
			if err != nil {
				return "", err
			}
			return mapping.FrameworkType, nil
		},
		// tfTypeValue returns the TF framework value constructor
		"tfTypeValue": func(a pkg.MergedAlias) (string, error) {
			mapping, err := terraformTypeFor(a.Type)
			if err != nil {
				return "", err
			}
			return mapping.ValueFunction, nil
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
			return strings.Join(modifiers, ", ")
		},
		"planModifierField": planModifierField,
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
		"schemaType": func(a pkg.MergedAlias) (string, error) {
			mapping, err := terraformTypeFor(a.Type)
			if err != nil {
				return "", err
			}
			return mapping.SchemaType, nil
		},
		"objectSchemaType": func(a pkg.MergedObjectAttribute) string {
			mapping, _ := terraformTypeFor(a.Type)
			return mapping.SchemaType
		},
		"objectAttrType": func(a pkg.MergedObjectAttribute) string {
			mapping, _ := terraformTypeFor(a.Type)
			return mapping.FrameworkType
		},
		"objectPlanModifierField": func(a pkg.MergedObjectAttribute) string {
			return planModifierField(pkg.MergedAlias{
				Type:      a.Type,
				Immutable: a.Immutable,
				Computed:  a.Computed,
			})
		},
	}
}

func planModifierField(alias pkg.MergedAlias) string {
	if !alias.Immutable && !alias.Computed {
		return ""
	}
	modifierType := ""
	modifierPackage := ""
	switch mapping, err := terraformTypeFor(alias.Type); {
	case err != nil:
		return ""
	case mapping.SchemaType == "String":
		modifierType, modifierPackage = "String", "stringplanmodifier"
	case mapping.SchemaType == "Bool":
		modifierType, modifierPackage = "Bool", "boolplanmodifier"
	case mapping.SchemaType == "Int64":
		modifierType, modifierPackage = "Int64", "int64planmodifier"
	case mapping.SchemaType == "List":
		modifierType, modifierPackage = "List", "listplanmodifier"
	case mapping.SchemaType == "ListNested":
		modifierType, modifierPackage = "List", "listplanmodifier"
	case mapping.SchemaType == "Map":
		modifierType, modifierPackage = "Map", "mapplanmodifier"
	default:
		return ""
	}

	var modifiers []string
	if alias.Immutable {
		modifiers = append(modifiers, modifierPackage+".RequiresReplace()")
	}
	if alias.Computed {
		modifiers = append(modifiers, modifierPackage+".UseStateForUnknown()")
	}
	return fmt.Sprintf("PlanModifiers: []planmodifier.%s{%s},", modifierType, strings.Join(modifiers, ", "))
}

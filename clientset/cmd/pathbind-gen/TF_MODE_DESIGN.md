# TF Mode Code Generation Design Document

## Overview

This document outlines the design for implementing TF mode in pathbind-gen to generate Terraform framework resource code from pathbind configuration files.

## Context

### Current State (terraform-provider-rhcs)
- Manual resource implementation with 478+ lines of boilerplate per resource
- State struct with only `tfsdk:` tags
- Hand-written Schema() function
- Manual CRUD scaffolding with no code generation
- Duplicated mapping logic (buildNodePoolSpec, populateState, etc.)

### Desired State (Post-pathbind TF mode)
- Generated Input struct with both `hfsdk:` and `tfsdk:` tags
- Generated Resource struct with auto-scaffolded CRUD methods
- Handler interface for consumer-specific logic (PreExpand, PostExpand, PostResponse)
- Auto-generated Schema() from draft + overrides with plan modifiers
- Uses pathbind.Expand/Flatten for type mapping

---

## Architecture

### Generation Pipeline

```
pathbind-gen --mode=tf \
  --draft=pathbind-draft.yaml \
  --overrides=pathbind-overrides.yaml \
  --output-dir=provider/hyperfleet/pathbind

↓ (for each resource: cluster, nodepool, oidcconfig)

├─ {resource}_input_gen.go
│  └─ XxxInput struct with hfsdk/tfsdk tags
│
├─ {resource}_resource_gen.go
│  ├─ XxxHandler interface (PreExpand, PostExpand, PostResponse)
│  ├─ XxxResource struct with Schema(), Create(), Read(), Update(), Delete(), ImportState()
│  └─ CRUD scaffolding with handler hooks
│
└─ {resource}_schema_gen.go
   └─ Schema attribute definitions (optional, may be embedded in resource_gen.go)
```

### Key Design Decisions

#### 1. Three-File Structure per Resource

**File 1: input_gen.go** — Input struct
- Purpose: Map Terraform state → SDK struct via pathbind
- Contains: Single struct with both `tfsdk:` and `hfsdk:` tags
- Imports: types, pathbind
- Size: ~30-50 lines

**File 2: resource_gen.go** — Resource base + handler interface + CRUD
- Purpose: Generated scaffolding with handler hooks
- Contains:
  - XxxHandler interface (3 methods)
  - XxxResource struct (Client, AccountID, CallerARN, Handler fields)
  - Schema() method (returns schema.Schema)
  - Create/Read/Update/Delete/ImportState methods with handler calls
- Imports: resource, schema, planmodifier, types, pathbind, SDK types, diag
- Size: ~300-400 lines per resource

**File 3: schema_gen.go** (Optional)
- Purpose: Keep schema definition separate if it's large
- Alternative: Embed schema inline in resource_gen.go as helper function
- Decision: Embed for now (simpler imports, keeps related code together)

#### 2. Input Struct Design

```go
// Generated: cluster_input_gen.go
type ClusterInput struct {
    // SDK-mapped fields: have both tfsdk and hfsdk tags
    Name                types.String `tfsdk:"name" hfsdk:"metadata.name"`
    AWSSubnetIDs        types.List   `tfsdk:"aws_subnet_ids" hfsdk:"spec.hostedCluster.platform.aws.cloudProviderConfig.subnet.id"`
    VPCID               types.String `tfsdk:"vpc_id" hfsdk:"spec.hostedCluster.platform.aws.cloudProviderConfig.vpc"`
    AvailabilityZones   types.List   `tfsdk:"availability_zones" hfsdk:"spec.hostedCluster.platform.aws.cloudProviderConfig.zone"`
    CloudRegion         types.String `tfsdk:"cloud_region" hfsdk:"spec.hostedCluster.platform.aws.region"`
    ExpirationTimestamp types.String `tfsdk:"expiration_timestamp" hfsdk:"spec.expirationTimestamp"`
    DeleteProtection    types.Bool   `tfsdk:"delete_protection" hfsdk:"spec.deleteProtection"`
    Tags                types.Map    `tfsdk:"tags" hfsdk:"spec.tags"`
    
    // Consumer-only fields: hfsdk:"-" means pathbind skips them
    OperatorRolesPrefix types.String `tfsdk:"operator_roles_prefix" hfsdk:"-"`
    AWSPartition        types.String `tfsdk:"aws_partition" hfsdk:"-"`
    
    // Note: Computed/ID fields go in separate State struct (not in Input)
    // Consumer keeps existing state.go unchanged
}
```

**Key Points:**
- Uses Terraform framework types (types.String, types.List, types.Bool, types.Map)
- Same struct used for both Create and Update plans
- Consumer-only fields have `hfsdk:"-"` so pathbind skips them
- Pathbind.Expand(ClusterInput, &v1alpha1.Cluster) does the mapping

#### 3. Handler Interface Design

```go
// Generated: cluster_resource_gen.go (in same file as Resource struct)
type ClusterHandler interface {
    // PreExpand: validation + derivation before Expand()
    // Called before pathbind.Expand(), allowing consumer to validate and compute derived fields
    // Example: validate name length, derive region from AZs, compute RolesRef from prefix
    PreExpand(ctx context.Context, input *ClusterInput) diag.Diagnostics
    
    // PostExpand: set SDK fields that pathbind cannot express
    // Called after pathbind.Expand(), before API call
    // Example: set enum constants, computed structs (RolesRef with 7 ARNs), platform type
    PostExpand(ctx context.Context, input *ClusterInput, obj *v1alpha1.Cluster) diag.Diagnostics
    
    // PostResponse: output formatting + side effects after API response
    // Called after Create/Read/Update/Delete API calls
    // Example: patch auto_repair quirk, format OIDC issuer URL, set phase
    PostResponse(ctx context.Context, resp *v1alpha1.Cluster) diag.Diagnostics
}
```

**Rationale:**
- One interface per resource (not per operation) because TF uses same Input struct for create/update
- Three methods map cleanly to TF Framework request/response lifecycle
- PreExpand ≈ cobra's PreRequest
- PostExpand ≈ cobra's PostExpand
- PostResponse ≈ cobra's PostResponse
- No PreDelete because Delete doesn't use Input struct

#### 4. Resource Struct Design

```go
// Generated: cluster_resource_gen.go
type ClusterResource struct {
    // Client, AccountID, CallerARN set by consumer via Configure()
    Client    hyperfleet.Interface
    AccountID string
    CallerARN string
    
    // Handler implementation provided by consumer
    Handler ClusterHandler
}

func (r *ClusterResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_cluster_hyperfleet"
}

func (r *ClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
    resp.Schema = schema.Schema{
        Description: "...",
        Attributes: map[string]schema.Attribute{
            "id": schema.StringAttribute{
                Description: "Cluster UID assigned by the Platform API on creation.",
                Computed:    true,
                PlanModifiers: []planmodifier.String{
                    stringplanmodifier.UseStateForUnknown(),
                },
            },
            "name": schema.StringAttribute{
                Description: "Human-readable cluster name. Immutable after creation.",
                Required:    true,
                PlanModifiers: []planmodifier.String{
                    stringplanmodifier.RequiresReplace(),
                },
            },
            // ... auto-generated from overrides ...
        },
    }
}

func (r *ClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
    // Scaffolding flow:
    // 1. Get plan into ClusterInput
    // 2. Call Handler.PreExpand() for validation + derivation
    // 3. Create empty SDK struct: &v1alpha1.Cluster{}
    // 4. Call pathbind.Expand(input, sdkStruct) to map fields
    // 5. Call Handler.PostExpand() to set unmappable fields
    // 6. Call API: r.Client.HyperfleetV1alpha1().Clusters().Create(ctx, sdkStruct, ...)
    // 7. Call pathbind.Flatten(apiResp, state) to map back to state
    // 8. Call Handler.PostResponse() for output formatting
    // 9. Set state in response
    ...
}

func (r *ClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
    // Scaffolding flow:
    // 1. Get state
    // 2. Call API: r.Client.HyperfleetV1alpha1().Clusters().Get(ctx, state.ID, ...)
    // 3. Call pathbind.Flatten(apiResp, state)
    // 4. Call Handler.PostResponse()
    // 5. Set state in response
    ...
}

func (r *ClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
    // Scaffolding flow:
    // 1. Get plan into ClusterInput
    // 2. Get current state
    // 3. Call Handler.PreExpand()
    // 4. Fetch live object from API: r.Client.HyperfleetV1alpha1().Clusters().Get(ctx, state.ID, ...)
    // 5. Call pathbind.Expand(plan, liveObject) — overwrites only plan fields, preserves the rest
    // 6. Call Handler.PostExpand()
    // 7. Call API: r.Client.HyperfleetV1alpha1().Clusters().Update(ctx, liveObject, ...)
    // 8. Call pathbind.Flatten(apiResp, state)
    // 9. Call Handler.PostResponse()
    // 10. Set state in response
    ...
}

func (r *ClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
    // Scaffolding flow:
    // 1. Get state
    // 2. Call Handler.PreExpand() for validation (no-op for delete, but allows unified interface)
    // 3. Call API: r.Client.HyperfleetV1alpha1().Clusters().Delete(ctx, state.ID, ...)
    // 4. Call Handler.PostResponse() for side effects
    ...
}

func (r *ClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
    // Scaffolding flow:
    // 1. ID passed in req.ID
    // 2. Call API: r.Client.HyperfleetV1alpha1().Clusters().Get(ctx, req.ID, ...)
    // 3. Call pathbind.Flatten(apiResp, state)
    // 4. Call Handler.PostResponse()
    // 5. Set state in response
    ...
}
```

#### 5. Schema Generation from Overrides

Override YAML drives schema generation:

```yaml
resources:
  cluster:
    aliases:
      - path: metadata.name
        attr: name
        type: string
        required: true
        immutable: true
        description: "Human-readable cluster name. Immutable after creation."
      
      - path: spec.deleteProtection
        attr: delete_protection
        type: bool
        computed: false
        immutable: false
        description: "Enable delete protection."
      
      - attr: operator_roles_prefix          # No path → consumer-only
        type: string
        required: true
        immutable: true
        description: "IAM role prefix."
```

Generated schema logic:
1. For each alias with a path: generate schema Attribute
2. Type mapping: string → StringAttribute, bool → BoolAttribute, etc.
3. Plan modifiers:
   - immutable: true → RequiresReplace()
   - computed: true → UseStateForUnknown()
   - sensitive: true → Sensitive()
4. Required: set on Attribute
5. Description: from alias description

---

## Template Files

### File 1: input.go.tmpl

**Purpose:** Generate Input struct with hfsdk/tfsdk tags

**Inputs:** TFTemplateData
- ResourceName
- AllFields ([]MergedAlias with hfsdk/tfsdk info)

**Output:** 30-50 lines

**Structure:**
```go
// Code generated by pathbind-gen --mode=tf. DO NOT EDIT.
package {{.Package}}

import (
    "github.com/hashicorp/terraform-plugin-framework/types"
)

type {{.ResourceName}}Input struct {
    {{range .AllFields}}
    {{.GoName}} {{tfType .}} `tfsdk:"{{attrName .}}" hfsdk:"{{.Path}}"`
    {{end}}
}
```

**Template Functions Needed:**
- `tfType(MergedAlias) string` — map Go type to framework type
- `attrName(MergedAlias) string` — kebab-case attribute name

---

### File 2: resource.go.tmpl

**Purpose:** Generate Resource struct, Handler interface, CRUD scaffolding

**Inputs:** TFTemplateData
- ResourceName
- SDKType, SDKShortType
- AllFields, CreateFields, UpdateFields
- ImmutableList, ComputedList

**Output:** 300-400 lines

**Components:**
1. Package + imports
2. Handler interface definition (3 methods)
3. Resource struct definition (4 fields)
4. Metadata() method
5. Schema() method with attribute generation
6. Create() scaffolding with handler hooks
7. Read() scaffolding with handler hooks
8. Update() scaffolding with handler hooks
9. Delete() scaffolding with handler hooks
10. ImportState() scaffolding with handler hooks

**Key Implementation Details:**

For Schema():
- Iterate AllFields
- For each field:
  - Required? Set Required: true
  - Immutable? Add RequiresReplace() planmodifier
  - Computed? Add UseStateForUnknown() planmodifier
  - Sensitive? Add Sensitive() planmodifier
  - Description? From alias

For Create():
```
1. var input {{.ResourceName}}Input
2. resp.Diagnostics.Append(req.Plan.Get(ctx, &input)...)
3. dg := r.Handler.PreExpand(ctx, &input)
4. resp.Diagnostics.Append(dg...)
5. obj := &{{.SDKType}}{}
6. if err := pathbind.Expand(ctx, input, obj); err != nil { /* error */ }
7. dg = r.Handler.PostExpand(ctx, &input, obj)
8. resp.Diagnostics.Append(dg...)
9. obj.ObjectMeta.Name = input.Name.ValueString()  // or from plan
10. created, err := r.Client.HyperfleetV1alpha1().{{pluralize .ResourceName}}().Create(ctx, obj, ...)
11. if err != nil { /* error */ }
12. var state {{.ResourceName}}State
13. if err := pathbind.Flatten(ctx, created, &state); err != nil { /* error */ }
14. dg = r.Handler.PostResponse(ctx, created)
15. resp.Diagnostics.Append(dg...)
16. resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
```

For Update():
```
Similar to Create but:
- Fetch current state
- Call API.Get to get live object
- Call pathbind.Expand(plan, liveObject) — this overwrites only plan fields
- This preserves immutable fields the user didn't touch
```

For Delete():
```
1. var state {{.ResourceName}}State
2. resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
3. dg := r.Handler.PreExpand(ctx, nil)
4. resp.Diagnostics.Append(dg...)
5. err := r.Client.HyperfleetV1alpha1().{{pluralize .ResourceName}}().Delete(ctx, state.ID.ValueString(), ...)
6. if err != nil { /* error */ }
7. dg = r.Handler.PostResponse(ctx, nil)
8. resp.Diagnostics.Append(dg...)
```

**Template Functions Needed:**
- `pluralize(string) string` — Clusters() vs Clusters()
- `planModifiers(MergedAlias) []planmodifier.String` — generate modifiers for field
- `schemaAttribute(MergedAlias) string` — generate schema.StringAttribute, etc.

---

### File 3: schema.go.tmpl (Optional)

If schema is too large, split into separate file. For now, embed in resource.go.tmpl.

---

## Code Generation Orchestration

### tf/gen.go Structure

```go
func Run(draftPath, overridesPath, outputDir string) error {
    // 1. Load overrides YAML
    rawOv := loadOverrides(overridesPath)
    
    // 2. Validate required config
    cfg := rawOv.Config
    if cfg.Package == "" || cfg.TFProviderPkg == "" {
        error: "overrides config must set package and tfProviderPkg"
    }
    
    // 3. Load and index draft
    draftIndex := map[string]map[string]pkg.DraftField{}
    draftSDKTypes := map[string]string{}
    pkg.LoadDraft(draftPath, draftIndex, draftSDKTypes)
    
    // 4. Create output dir
    os.MkdirAll(outputDir, 0o755)
    
    // 5. Parse templates
    inputTmpl := template.Must(template.New("input").Parse(inputTemplate))
    resourceTmpl := template.Must(template.New("resource").Parse(resourceTemplate))
    
    // 6. For each resource (cluster, nodepool, oidcconfig):
    for _, resKey := range pkg.UnionKeys(draftSDKTypes, rawOv.Resources) {
        // a. Get overrides for this resource
        ovRes := rawOv.Resources[resKey]
        
        // b. Get SDK type
        sdkType := draftSDKTypes[resKey]  // or lookup default
        
        // c. Merge draft fields with overrides
        aliases := pkg.BuildMergedAliases(draftIndex[resKey], ovRes.Aliases)
        
        // d. Categorize into create/update
        createFields, _, _, updateFields, _, _ := pkg.CategorizeAliases(aliases)
        
        // e. Collect immutable/computed fields
        immutableList, computedList := collectFieldAttributes(aliases)
        
        // f. Build template data
        td := pkg.TFTemplateData{
            GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
            Package:       cfg.Package,
            ResourceName:  pkg.TitleCase(resKey),
            SDKType:       sdkType,
            SDKShortType:  pkg.SDKShortType(sdkType),
            AllFields:     aliases,
            CreateFields:  createFields,
            UpdateFields:  updateFields,
            ImmutableList: immutableList,
            ComputedList:  computedList,
        }
        
        // g. Emit three files
        emitFile(inputTmpl, td, "cluster_input_gen.go")
        emitFile(resourceTmpl, td, "cluster_resource_gen.go")
        // schema.go optional
    }
    
    return nil
}
```

---

## Consumer Integration

### What Consumer Needs to Provide

1. **Handler implementation:** Implement XxxHandler interface
   ```go
   type MyClusterHandler struct {}
   
   func (h *MyClusterHandler) PreExpand(ctx context.Context, input *pathbind.ClusterInput) diag.Diagnostics {
       // Validate name length, derive region, compute RolesRef
   }
   
   func (h *MyClusterHandler) PostExpand(ctx context.Context, input *pathbind.ClusterInput, obj *v1alpha1.Cluster) diag.Diagnostics {
       // Set enum constants, computed structs
   }
   
   func (h *MyClusterHandler) PostResponse(ctx context.Context, resp *v1alpha1.Cluster) diag.Diagnostics {
       // Patch auto_repair quirk
   }
   ```

2. **Resource wrapping:** Embed generated resource
   ```go
   type ClusterResource struct {
       *pathbind.ClusterResource
       handler *MyClusterHandler
   }
   
   func New() resource.Resource {
       return &ClusterResource{
           handler: &MyClusterHandler{},
       }
   }
   
   func (r *ClusterResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
       r.ClusterResource = &pathbind.ClusterResource{
           Client:    shared.HyperfleetClient,
           AccountID: shared.HyperfleetAccountID,
           CallerARN: shared.HyperfleetCallerARN,
           Handler:   r.handler,
       }
   }
   ```

3. **State struct:** Keep existing (or use new Input struct with state tag variants)
   - Option A: Keep separate State struct with only `tfsdk:` tags (backward compatible)
   - Option B: Use Input struct for both (requires `tfsdk:` support in Input)
   - Recommended: Option A (no breaking change to existing code)

### pathbind-overrides.yaml Mapping

```yaml
config:
  package: pathbind                          # Generated code package
  tfProviderPkg: github.com/.../terraform-provider-rhcs/provider/hyperfleet
  runtimePkg: github.com/.../terraform-provider-rhcs/provider/providerdata  # For cobra only
  runtimeType: ProviderSharedData             # For cobra only

resources:
  cluster:
    aliases:
      - path: metadata.name
        attr: name
        type: string
        required: true
        immutable: true
        description: "Human-readable cluster name. Immutable after creation."
      
      - path: spec.deleteProtection
        attr: delete_protection
        type: bool
        description: "Enable delete protection."
      
      - attr: operator_roles_prefix
        type: string
        required: true
        immutable: true
        description: "IAM role prefix."
        hfsdk: "-"  # Explicit consumer-only marker (optional, path="" implies it)
```

---

## File Structure Summary

```
pathbind-gen/
├── tf/
│   ├── gen.go (150-200 lines)
│   │   ├── Run() orchestration
│   │   ├── loadOverrides()
│   │   ├── emitFile()
│   │   └── buildFuncMap()
│   │
│   ├── templates.go (12 lines)
│   │   ├── //go:embed templates/input.go.tmpl
│   │   ├── //go:embed templates/resource.go.tmpl
│   │   └── //go:embed templates/schema.go.tmpl
│   │
│   └── templates/
│       ├── input.go.tmpl (50 lines)
│       ├── resource.go.tmpl (350 lines)
│       └── schema.go.tmpl (optional, 100-150 lines)
│
└── main.go
    └── runTF() — calls tf.Run()
```

---

## Template Functions Reference

### buildFuncMap() needs:

```go
"lower": strings.ToLower
"contains": func([]string, string) bool
"tfType": func(MergedAlias) string {
    // string → types.StringType
    // bool → types.BoolType
    // int32, int64 → types.Int64Type
    // etc.
}
"attrName": func(MergedAlias) string {
    // GoName → kebab-case (ToKebab)
}
"planModifiers": func(MergedAlias) string {
    // Immutable → RequiresReplace()
    // Computed → UseStateForUnknown()
    // Sensitive → Sensitive()
}
"schemaAttrType": func(MergedAlias) string {
    // string → "schema.StringAttribute"
    // bool → "schema.BoolAttribute"
    // list → "schema.ListAttribute"
}
"pluralize": func(string) string {
    // Cluster → Clusters
    // NodePool → NodePools
}
"isConsumerOnly": func(MergedAlias) bool {
    // Path == "-"
}
```

---

## Open Questions / Future Considerations

1. **State struct handling:**
   - Keep separate from Input? (Recommended, backward compatible)
   - Or merge into one struct with dual tags?

2. **Handler method signatures:**
   - Should PostResponse receive (resp *v1alpha1.Cluster) or (*State)?
   - Current plan: Receive SDK struct, consumer patches State separately

3. **Error handling in handlers:**
   - Return diag.Diagnostics for rich error reporting?
   - Or return error? (Current plan: diag.Diagnostics)

4. **Nested blocks/attributes:**
   - How to handle complex nested types (e.g., aws_node_pool)?
   - Schema generation strategy for nested structures?
   - Deferred to Phase 3

5. **List/Map attribute handling:**
   - How to convert types.List ↔ []string in Expand/Flatten?
   - Pathbind reflection handles this, but worth documenting

---

## Implementation Order

1. **Step 1:** Implement tf/gen.go (orchestration only, no templates)
2. **Step 2:** Implement input.go.tmpl (simple, struct only)
3. **Step 3:** Implement resource.go.tmpl (largest, CRUD scaffolding)
4. **Step 4:** Test generation produces valid Go code
5. **Step 5:** Integrate with main.go runTF()
6. **Step 6:** Consumer-side testing (terraform-provider-rhcs)

---

## Success Criteria

- [ ] pathbind-gen --mode=tf generates three files per resource
- [ ] Generated input_gen.go compiles and has correct hfsdk/tfsdk tags
- [ ] Generated resource_gen.go compiles with valid CRUD methods
- [ ] Generated schema matches terraform-provider-rhcs patterns
- [ ] Handler interface can be implemented by consumer
- [ ] Consumer can wire generated resource into provider
- [ ] Consumer integration test passes (create/read/update/delete)


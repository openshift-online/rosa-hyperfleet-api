package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type leafPath struct {
	path   string
	goType string
}

type openAPIWalker struct {
	schemas     map[string]interface{}
	diagnostics []string
}

func newOpenAPIWalker(path string) (*openAPIWalker, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing openapi yaml: %w", err)
	}
	components, _ := doc["components"].(map[string]interface{})
	if components == nil {
		return nil, fmt.Errorf("openapi yaml has no components section")
	}
	schemas, _ := components["schemas"].(map[string]interface{})
	if schemas == nil {
		return nil, fmt.Errorf("openapi yaml has no components.schemas section")
	}
	return &openAPIWalker{schemas: schemas}, nil
}

func (w *openAPIWalker) resolve(ref string) map[string]interface{} {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return nil
	}
	name := strings.TrimPrefix(ref, prefix)
	schema, _ := w.schemas[name].(map[string]interface{})
	return schema
}

func (w *openAPIWalker) effective(node map[string]interface{}) map[string]interface{} {
	return w.effectiveWithVisited(node, make(map[string]bool))
}

func (w *openAPIWalker) effectiveWithVisited(node map[string]interface{}, visited map[string]bool) map[string]interface{} {
	if node == nil {
		return nil
	}
	if ref, ok := node["$ref"].(string); ok {
		if visited[ref] {
			w.diagnostics = append(w.diagnostics, fmt.Sprintf("schema cycle detected: %s", ref))
			return nil
		}
		resolved := w.resolve(ref)
		if resolved == nil {
			return nil
		}
		visited[ref] = true
		return w.effectiveWithVisited(resolved, visited)
	}
	if allOf, ok := node["allOf"].([]interface{}); ok {
		merged := map[string]interface{}{}
		if props, ok := node["properties"].(map[string]interface{}); ok {
			merged["properties"] = props
		}
		if t, ok := node["type"]; ok {
			merged["type"] = t
		}
		if desc, ok := node["description"]; ok {
			merged["description"] = desc
		}
		for _, item := range allOf {
			sub, _ := item.(map[string]interface{})
			if sub == nil {
				continue
			}
			eff := w.effectiveWithVisited(sub, visited)
			if eff == nil {
				continue
			}
			if props, ok := eff["properties"].(map[string]interface{}); ok {
				existing, _ := merged["properties"].(map[string]interface{})
				if existing == nil {
					existing = map[string]interface{}{}
				}
				for k, v := range props {
					existing[k] = v
				}
				merged["properties"] = existing
			}
			if _, hasType := merged["type"]; !hasType {
				if t, ok := eff["type"]; ok {
					merged["type"] = t
				}
			}
		}
		if len(merged) > 0 {
			return merged
		}
	}
	return node
}

func (w *openAPIWalker) navigateTo(rootSchemaName string, pathSegs []string) map[string]interface{} {
	cur, _ := w.schemas[rootSchemaName].(map[string]interface{})
	if cur == nil {
		return nil
	}
	cur = w.effective(cur)
	for _, seg := range pathSegs {
		if cur == nil {
			return nil
		}
		props, _ := cur["properties"].(map[string]interface{})
		if props == nil {
			return nil
		}
		next, _ := props[seg].(map[string]interface{})
		cur = w.effective(next)
	}
	return cur
}

func (w *openAPIWalker) goTypeFromSchema(schema map[string]interface{}) string {
	if schema == nil {
		return ""
	}
	typ, _ := schema["type"].(string)
	switch typ {
	case "string":
		return "string"
	case "boolean":
		return "boolean"
	case "integer":
		format, _ := schema["format"].(string)
		if format == "int64" {
			return "integer(int64)"
		}
		return "integer(int32)"
	case "number":
		return "number"
	}
	return ""
}

const maxDepth = 12

func (w *openAPIWalker) expandLeaves(rootSchemaName, fieldPath string) []leafPath {
	segs := strings.Split(fieldPath, ".")
	node := w.navigateTo(rootSchemaName, segs)
	if node == nil {
		w.diagnostics = append(w.diagnostics, fmt.Sprintf("unresolved schema path: %s.%s", rootSchemaName, fieldPath))
		return nil
	}
	var out []leafPath
	w.walkNode(node, fieldPath, &out, 0)
	if len(out) == 0 {
		w.diagnostics = append(w.diagnostics, fmt.Sprintf("schema path has no scalar leaves: %s.%s", rootSchemaName, fieldPath))
		return nil
	}
	return out
}

func (w *openAPIWalker) walkNode(node map[string]interface{}, currentPath string, out *[]leafPath, depth int) {
	if depth > maxDepth || node == nil {
		return
	}
	node = w.effective(node)
	if node == nil {
		return
	}
	if gt := w.goTypeFromSchema(node); gt != "" {
		*out = append(*out, leafPath{path: currentPath, goType: gt})
		return
	}
	typ, _ := node["type"].(string)
	if typ == "array" {
		*out = append(*out, leafPath{path: currentPath, goType: "array"})
		return
	}
	if _, hasAdditional := node["additionalProperties"]; hasAdditional {
		*out = append(*out, leafPath{path: currentPath, goType: "map"})
		return
	}
	props, _ := node["properties"].(map[string]interface{})
	if len(props) == 0 {
		*out = append(*out, leafPath{path: currentPath, goType: "string"})
		return
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	for _, k := range keys {
		child, _ := props[k].(map[string]interface{})
		w.walkNode(child, currentPath+"."+k, out, depth+1)
	}
}

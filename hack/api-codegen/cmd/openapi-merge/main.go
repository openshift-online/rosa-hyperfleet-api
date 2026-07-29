package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var mergeTypes = map[string]bool{
	"ClusterSpec":                  true,
	"NodePoolSpec":                 true,
	"ClusterConfiguration":         true,
	"KubeletConfig":                true,
	"MachineConfigSpec":            true,
	"HostedClusterSpecPassthrough": true,
}

func main() {
	generatedPath := flag.String("generated", "", "Path to generated Swagger 2.0 JSON definitions")
	specPath := flag.String("spec", "", "Path to openapi.yaml to update")
	flag.Parse()

	if *generatedPath == "" || *specPath == "" {
		log.Fatal("--generated and --spec are required")
	}

	genData, err := os.ReadFile(*generatedPath)
	if err != nil {
		log.Fatalf("reading generated file: %v", err)
	}

	var swagger struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err := json.Unmarshal(genData, &swagger); err != nil {
		log.Fatalf("parsing generated JSON: %v", err)
	}

	schemas := make(map[string]*yaml.Node)
	for name, raw := range swagger.Definitions {
		if !mergeTypes[name] {
			continue
		}
		node, err := swaggerDefToOAS3(name, raw)
		if err != nil {
			log.Fatalf("converting %s: %v", name, err)
		}
		schemas[name] = node
	}

	specData, err := os.ReadFile(*specPath)
	if err != nil {
		log.Fatalf("reading spec: %v", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(specData, &doc); err != nil {
		log.Fatalf("parsing spec YAML: %v", err)
	}

	schemasNode := findSchemasNode(&doc)
	if schemasNode == nil {
		log.Fatal("could not find components.schemas in spec")
	}

	replaced := 0
	for i := 0; i < len(schemasNode.Content)-1; i += 2 {
		keyNode := schemasNode.Content[i]
		if mergeTypes[keyNode.Value] {
			if node, ok := schemas[keyNode.Value]; ok {
				schemasNode.Content[i+1] = node
				replaced++
			}
		}
	}

	for name, node := range schemas {
		found := false
		for i := 0; i < len(schemasNode.Content)-1; i += 2 {
			if schemasNode.Content[i].Value == name {
				found = true
				break
			}
		}
		if !found {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: name}
			schemasNode.Content = append(schemasNode.Content, keyNode, node)
			replaced++
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		log.Fatalf("marshaling updated spec: %v", err)
	}
	enc.Close()

	if err := os.WriteFile(*specPath, buf.Bytes(), 0644); err != nil {
		log.Fatalf("writing spec: %v", err)
	}

	fmt.Printf("openapi-merge: replaced/added %d schemas in %s\n", replaced, *specPath)
}

func findSchemasNode(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(doc.Content)-1; i += 2 {
		if doc.Content[i].Value == "components" {
			comp := doc.Content[i+1]
			if comp.Kind != yaml.MappingNode {
				return nil
			}
			for j := 0; j < len(comp.Content)-1; j += 2 {
				if comp.Content[j].Value == "schemas" {
					return comp.Content[j+1]
				}
			}
		}
	}
	return nil
}

var markerRe = regexp.MustCompile(`(?m)^\+[a-zA-Z].*$`)

var passthroughRefs = map[string]map[string]string{
	"ClusterSpec": {"hostedCluster": "HostedClusterSpecPassthrough"},
}

func swaggerDefToOAS3(typeName string, raw json.RawMessage) (*yaml.Node, error) {
	var def map[string]interface{}
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, err
	}

	cleanDescription(def)
	inlineSelfRefs(typeName, def)
	linkPassthroughRefs(typeName, def)
	convertRefs(def)

	node := toYAMLNode(def)
	return node, nil
}

func linkPassthroughRefs(typeName string, m map[string]interface{}) {
	fieldMap, ok := passthroughRefs[typeName]
	if !ok {
		return
	}
	props, ok := m["properties"].(map[string]interface{})
	if !ok {
		return
	}
	for field, targetType := range fieldMap {
		pm, ok := props[field].(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasRef := pm["$ref"]; hasRef {
			continue
		}
		pm["$ref"] = "#/definitions/" + targetType
		delete(pm, "type")
		delete(pm, "additionalProperties")
	}
}

func inlineSelfRefs(typeName string, m map[string]interface{}) {
	props, ok := m["properties"].(map[string]interface{})
	if !ok {
		return
	}
	selfRef := "#/definitions/" + typeName
	for field, v := range props {
		pm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if ref, ok := pm["$ref"].(string); ok && ref == selfRef {
			delete(pm, "$ref")
			pm["type"] = "object"
			pm["additionalProperties"] = true
			log.Printf("inlined self-ref %s.%s → type: object", typeName, field)
		}
	}
}

func toYAMLNode(v interface{}) *yaml.Node {
	switch val := v.(type) {
	case map[string]interface{}:
		node := &yaml.Node{Kind: yaml.MappingNode}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
			valNode := toYAMLNode(val[k])
			node.Content = append(node.Content, keyNode, valNode)
		}
		return node
	case []interface{}:
		node := &yaml.Node{Kind: yaml.SequenceNode}
		for _, item := range val {
			node.Content = append(node.Content, toYAMLNode(item))
		}
		return node
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: val}
	case float64:
		if val == float64(int64(val)) {
			return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d", int64(val)), Tag: "!!int"}
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%g", val)}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%t", val), Tag: "!!bool"}
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", val)}
	}
}

func cleanDescription(m map[string]interface{}) {
	if desc, ok := m["description"].(string); ok {
		lines := strings.Split(desc, "\n")
		var cleaned []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if markerRe.MatchString(trimmed) {
				continue
			}
			cleaned = append(cleaned, line)
		}
		result := strings.TrimSpace(strings.Join(cleaned, "\n"))
		if result == "" {
			delete(m, "description")
		} else {
			m["description"] = result
		}
	}

	if props, ok := m["properties"].(map[string]interface{}); ok {
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if pm, ok := props[k].(map[string]interface{}); ok {
				cleanDescription(pm)
			}
		}
	}

	if items, ok := m["items"].(map[string]interface{}); ok {
		cleanDescription(items)
	}

	if addl, ok := m["additionalProperties"].(map[string]interface{}); ok {
		cleanDescription(addl)
	}
}

func convertRefs(m map[string]interface{}) {
	if ref, ok := m["$ref"].(string); ok {
		m["$ref"] = strings.Replace(ref, "#/definitions/", "#/components/schemas/", 1)
	}

	for _, v := range m {
		switch val := v.(type) {
		case map[string]interface{}:
			convertRefs(val)
		case []interface{}:
			for _, item := range val {
				if im, ok := item.(map[string]interface{}); ok {
					convertRefs(im)
				}
			}
		}
	}
}

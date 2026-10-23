package main

import (
	"encoding/json"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// Label-read references must resolve to a response, not a schema with response-only fields.
func TestLabelResponseRefs(t *testing.T) {
	content, err := os.ReadFile("../../../../api/v1alpha1/public/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(content, &spec); err != nil {
		t.Fatal(err)
	}
	components := spec["components"].(map[string]any)
	responses, ok := components["responses"].(map[string]any)
	if !ok {
		t.Fatal("components.responses is missing")
	}
	response, ok := responses["ResourceLabels"].(map[string]any)
	if !ok {
		t.Fatal("components.responses.ResourceLabels is missing")
	}
	if _, hasSchema := components["schemas"].(map[string]any)["ResourceLabels"]; hasSchema {
		t.Fatal("ResourceLabels response must not be a schema")
	}
	schema := response["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema["type"] != "object" || schema["additionalProperties"].(map[string]any)["type"] != "string" {
		t.Fatalf("label response must be a string map, got %v", schema)
	}
	for _, collection := range []string{"clusters", "nodepools", "oidc_configs", "dns_reservations"} {
		path := "/" + collection + "/{id}/labels"
		get := spec["paths"].(map[string]any)[path].(map[string]any)["get"].(map[string]any)
		success := get["responses"].(map[string]any)["200"].(map[string]any)
		if success["$ref"] != "#/components/responses/ResourceLabels" {
			t.Errorf("%s label GET response reference = %v", path, success["$ref"])
		}
	}
}

func TestPreserveReadOnly(t *testing.T) {
	existingCluster := map[string]any{
		"properties": map[string]any{
			"proxy": map[string]any{"readOnly": true},
		},
	}
	generatedCluster := json.RawMessage(`{"properties":{"proxy":{"type":"object"}}}`)
	mergedCluster, err := preserveReadOnly(generatedCluster, existingCluster)
	if err != nil {
		t.Fatalf("preserve Cluster.proxy readOnly: %v", err)
	}
	assertReadOnly(t, mergedCluster, "properties", "proxy")

	existingList := map[string]any{
		"properties": map[string]any{
			"items": map[string]any{
				"items": map[string]any{
					"properties": map[string]any{
						"proxy": map[string]any{"readOnly": true},
					},
				},
			},
		},
	}
	generatedList := json.RawMessage(`{"properties":{"items":{"items":{"properties":{"proxy":{"type":"object"}}}}}}`)
	mergedList, err := preserveReadOnly(generatedList, existingList)
	if err != nil {
		t.Fatalf("preserve ClusterList.items.proxy readOnly: %v", err)
	}
	assertReadOnly(t, mergedList, "properties", "items", "items", "properties", "proxy")
}

func assertReadOnly(t *testing.T, data json.RawMessage, path ...string) {
	t.Helper()
	var schema any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("unmarshal merged schema: %v", err)
	}
	current := schema
	for _, part := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("schema path %v stopped at %q", path, part)
		}
		current, ok = object[part]
		if !ok {
			t.Fatalf("schema path %v missing %q", path, part)
		}
	}
	property, ok := current.(map[string]any)
	if !ok || property["readOnly"] != true {
		t.Errorf("schema path %v has readOnly=%v, want true", path, current)
	}
}

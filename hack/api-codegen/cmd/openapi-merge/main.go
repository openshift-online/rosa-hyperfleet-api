package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	var (
		specFile      string
		generatedFile string
		outputFile    string
		schemas       string
	)

	flag.StringVar(&specFile, "spec", "", "Path to the handwritten OpenAPI spec (YAML)")
	flag.StringVar(&generatedFile, "generated", "", "Path to the generated schema (JSON from openapi-gen)")
	flag.StringVar(&outputFile, "output", "", "Output file (defaults to overwriting -spec)")
	flag.StringVar(&schemas, "schemas", "ClusterSpec,NodePoolSpec", "Comma-separated schema names to replace")
	flag.Parse()

	if specFile == "" || generatedFile == "" {
		flag.Usage()
		os.Exit(1)
	}
	if outputFile == "" {
		outputFile = specFile
	}

	specData, err := os.ReadFile(specFile)
	if err != nil {
		log.Fatalf("reading spec: %v", err)
	}

	genData, err := os.ReadFile(generatedFile)
	if err != nil {
		log.Fatalf("reading generated: %v", err)
	}

	var genDoc struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err := json.Unmarshal(genData, &genDoc); err != nil {
		log.Fatalf("parsing generated JSON: %v", err)
	}

	schemaList := splitCSV(schemas)

	merged := 0
	result := specData
	for _, name := range schemaList {
		raw, ok := genDoc.Definitions[name]
		if !ok {
			log.Printf("warning: schema %q not found in generated output, skipping", name)
			continue
		}

		yamlBlock, err := jsonSchemaToYAML(raw, 6)
		if err != nil {
			log.Fatalf("converting %s to YAML: %v", name, err)
		}

		updated, found := replaceSchemaBlock(result, name, yamlBlock)
		if found {
			result = updated
			merged++
			log.Printf("replaced schema: %s", name)
		} else {
			result = insertSchemaBlock(result, name, yamlBlock)
			merged++
			log.Printf("inserted schema: %s", name)
		}
	}

	if merged == 0 {
		log.Fatal("no schemas were merged")
	}

	if err := os.WriteFile(outputFile, result, 0644); err != nil {
		log.Fatalf("writing output: %v", err)
	}

	fmt.Printf("Merged %d schemas into %s\n", merged, outputFile)
}

// replaceSchemaBlock finds a schema definition block in the YAML by looking
// for `    <name>:` at the expected indentation under components.schemas,
// and replaces everything from that line until the next sibling definition.
func replaceSchemaBlock(spec []byte, schemaName string, replacement []byte) ([]byte, bool) {
	lines := splitLines(spec)
	header := "    " + schemaName + ":"

	startIdx := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \r") == header {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return nil, false
	}

	endIdx := startIdx + 1
	for endIdx < len(lines) {
		line := lines[endIdx]
		if line == "" || strings.TrimSpace(line) == "" {
			break
		}
		indent := countLeadingSpaces(line)
		if indent <= 4 {
			break
		}
		endIdx++
	}

	var buf bytes.Buffer
	for _, line := range lines[:startIdx] {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	buf.WriteString(header)
	buf.WriteByte('\n')
	buf.Write(replacement)
	for _, line := range lines[endIdx:] {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}

	return buf.Bytes(), true
}

// insertSchemaBlock appends a new schema definition at the end of the
// components.schemas section (just before the next top-level YAML key or EOF).
func insertSchemaBlock(spec []byte, schemaName string, replacement []byte) []byte {
	lines := splitLines(spec)
	header := "    " + schemaName + ":"

	// Find the end of the schemas section: the last line at indent >= 4
	// after we've entered the schemas block.
	schemasStart := -1
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \r")
		if trimmed == "    schemas:" || trimmed == "  schemas:" {
			schemasStart = i
			break
		}
	}
	if schemasStart < 0 {
		log.Printf("warning: could not find schemas section for insertion of %s", schemaName)
		return spec
	}

	// Walk forward to find where the schemas section ends
	insertIdx := len(lines)
	for i := schemasStart + 1; i < len(lines); i++ {
		line := lines[i]
		if line == "" || strings.TrimSpace(line) == "" {
			continue
		}
		indent := countLeadingSpaces(line)
		if indent < 4 {
			insertIdx = i
			break
		}
	}

	var buf bytes.Buffer
	for _, line := range lines[:insertIdx] {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	// Add a blank separator only if the preceding line isn't already blank
	if insertIdx > 0 && strings.TrimSpace(lines[insertIdx-1]) != "" {
		buf.WriteByte('\n')
	}
	buf.WriteString(header)
	buf.WriteByte('\n')
	buf.Write(replacement)
	for _, line := range lines[insertIdx:] {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}

	return buf.Bytes()
}

// jsonSchemaToYAML converts a JSON schema object to a YAML block indented at
// the given base level (number of spaces for the first property level).
// It rewrites $ref paths from #/definitions/ to #/components/schemas/ for
// OpenAPI 3.0 compatibility.
func jsonSchemaToYAML(raw json.RawMessage, baseIndent int) ([]byte, error) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}

	rewriteRefs(obj)

	yamlBytes, err := yaml.Marshal(obj)
	if err != nil {
		return nil, err
	}

	prefix := strings.Repeat(" ", baseIndent)
	var buf bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(yamlBytes))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		buf.WriteString(prefix)
		buf.WriteString(line)
		buf.WriteByte('\n')
	}

	return buf.Bytes(), nil
}

// rewriteRefs recursively converts $ref paths from the internal
// #/definitions/ format to OpenAPI 3.0's #/components/schemas/.
func rewriteRefs(obj map[string]any) {
	for k, v := range obj {
		if k == "$ref" {
			if s, ok := v.(string); ok {
				obj[k] = strings.Replace(s, "#/definitions/", "#/components/schemas/", 1)
			}
		}
		switch val := v.(type) {
		case map[string]any:
			rewriteRefs(val)
		case []any:
			for _, item := range val {
				if m, ok := item.(map[string]any); ok {
					rewriteRefs(m)
				}
			}
		}
	}
}

func splitLines(data []byte) []string {
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func countLeadingSpaces(s string) int {
	n := 0
	for _, c := range s {
		if c == ' ' {
			n++
		} else {
			break
		}
	}
	return n
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	sort.Strings(out)
	return out
}

package hyperfleetdb

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/selection"
)

var validPathSegment = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func buildFieldSelectorFilter(sel fields.Selector, startParam int) (clauses []string, args []any, err error) {
	if sel == nil || sel.Empty() {
		return nil, nil, nil
	}

	paramIdx := startParam
	for _, req := range sel.Requirements() {
		clause, err := fieldRequirementToSQL(req.Field, req.Operator, paramIdx)
		if err != nil {
			return nil, nil, err
		}
		clauses = append(clauses, clause)
		args = append(args, req.Value)
		paramIdx++
	}
	return clauses, args, nil
}

func fieldRequirementToSQL(field string, op selection.Operator, paramIdx int) (string, error) {
	sqlOp, err := selectionOpToSQL(op)
	if err != nil {
		return "", err
	}

	col, err := fieldPathToSQL(field)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s %s $%d", col, sqlOp, paramIdx), nil
}

func selectionOpToSQL(op selection.Operator) (string, error) {
	switch op {
	case selection.Equals, selection.DoubleEquals:
		return "=", nil
	case selection.NotEquals:
		return "!=", nil
	default:
		return "", fmt.Errorf("pgruntime: unsupported field selector operator %q", op)
	}
}

func fieldPathToSQL(field string) (string, error) {
	parts := strings.SplitN(field, ".", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("pgruntime: invalid field selector %q — expected metadata.X, spec.X, or status.X", field)
	}

	root, rest := parts[0], parts[1]

	switch root {
	case "metadata":
		return metadataFieldToSQL(rest)
	case "spec":
		return jsonbPathToSQL("spec", rest)
	case "status":
		return jsonbPathToSQL("status", rest)
	default:
		return "", fmt.Errorf("pgruntime: unsupported field selector root %q — use metadata, spec, or status", root)
	}
}

func metadataFieldToSQL(field string) (string, error) {
	switch field {
	case "name":
		return "name", nil
	case "namespace":
		return "namespace", nil
	case "uid":
		// Compared as text so a malformed uid matches nothing instead of erroring.
		return "uid::text", nil
	default:
		return jsonbPathToSQL("metadata", field)
	}
}

func jsonbPathToSQL(column, path string) (string, error) {
	segments := strings.Split(path, ".")
	for _, seg := range segments {
		if !validPathSegment.MatchString(seg) {
			return "", fmt.Errorf("pgruntime: invalid field selector path segment %q", seg)
		}
	}

	if len(segments) == 1 {
		return fmt.Sprintf("%s->>'%s'", column, segments[0]), nil
	}

	var b strings.Builder
	b.WriteString(column)
	for i, seg := range segments {
		if i < len(segments)-1 {
			fmt.Fprintf(&b, "->'%s'", seg)
		} else {
			fmt.Fprintf(&b, "->>'%s'", seg)
		}
	}
	return b.String(), nil
}

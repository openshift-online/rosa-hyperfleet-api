package hyperfleetdb

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/validation"
)

var validPathSegment = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// buildFieldSelectorFilter converts a field selector into SQL WHERE clauses and
// bound arguments. For metadata.labels.* fields the label key is a bound
// parameter ($N) immediately before the value parameter ($N+1), so the key is
// never interpolated into SQL text.
func buildFieldSelectorFilter(sel fields.Selector, startParam int) (clauses []string, args []any, err error) {
	if sel == nil || sel.Empty() {
		return nil, nil, nil
	}

	paramIdx := startParam
	for _, req := range sel.Requirements() {
		clause, extraArgs, err := fieldRequirementToSQL(req.Field, req.Operator, paramIdx)
		if err != nil {
			return nil, nil, err
		}
		clauses = append(clauses, clause)
		args = append(args, extraArgs...)
		args = append(args, req.Value)
		paramIdx += len(extraArgs) + 1
	}
	return clauses, args, nil
}

// fieldRequirementToSQL returns the SQL clause for a single field-selector
// requirement. extraArgs holds any additional bound arguments that must be
// placed before the value argument (currently only the label key for
// metadata.labels.* paths).
func fieldRequirementToSQL(field string, op selection.Operator, paramIdx int) (clause string, extraArgs []any, err error) {
	sqlOp, err := selectionOpToSQL(op)
	if err != nil {
		return "", nil, err
	}

	col, extraArgs, err := fieldPathToSQL(field, paramIdx)
	if err != nil {
		return "", nil, err
	}

	valueParamIdx := paramIdx + len(extraArgs)
	return fmt.Sprintf("%s %s $%d", col, sqlOp, valueParamIdx), extraArgs, nil
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

// fieldPathToSQL returns the SQL column expression for a field selector path
// and any extra bound arguments that precede the value (e.g. the label key).
func fieldPathToSQL(field string, paramIdx int) (string, []any, error) {
	parts := strings.SplitN(field, ".", 2)
	if len(parts) < 2 {
		return "", nil, fmt.Errorf("pgruntime: invalid field selector %q — expected metadata.X, spec.X, or status.X", field)
	}

	root, rest := parts[0], parts[1]

	switch root {
	case "metadata":
		return metadataFieldToSQL(rest, paramIdx)
	case "spec":
		col, err := jsonbPathToSQL("spec", rest)
		return col, nil, err
	case "status":
		col, err := jsonbPathToSQL("status", rest)
		return col, nil, err
	default:
		return "", nil, fmt.Errorf("pgruntime: unsupported field selector root %q — use metadata, spec, or status", root)
	}
}

// metadataFieldToSQL returns the SQL column expression for a metadata.* path.
// For metadata.labels.* the label key is returned as an extra bound argument
// ($paramIdx) so it is never interpolated into SQL text.
func metadataFieldToSQL(field string, paramIdx int) (string, []any, error) {
	switch {
	case field == "name", field == "namespace":
		return field, nil, nil
	case strings.HasPrefix(field, "labels."):
		labelKey := strings.TrimPrefix(field, "labels.")
		if errs := validation.IsQualifiedName(labelKey); len(errs) > 0 {
			return "", nil, fmt.Errorf("pgruntime: invalid label key %q in field selector: %s", labelKey, strings.Join(errs, "; "))
		}
		// The label key is bound as $paramIdx; the value will be bound as the
		// next parameter by the caller.
		col := fmt.Sprintf("metadata->'labels'->>$%d", paramIdx)
		return col, []any{labelKey}, nil
	default:
		col, err := jsonbPathToSQL("metadata", field)
		return col, nil, err
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

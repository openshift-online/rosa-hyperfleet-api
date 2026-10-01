package hyperfleetdb

import (
	"encoding/json"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

// labelsCol is the JSONB expression the labels GIN index is built on
// (idx_resources_labels). Containment (@>) and key-exists (?) use the index.
const labelsCol = "metadata->'labels'"

// buildLabelSelectorFilter translates a label selector into SQL clauses with
// Kubernetes semantics: != and notin match objects that lack the key.
func buildLabelSelectorFilter(sel labels.Selector, startParam int) (clauses []string, args []any, err error) {
	if sel == nil || sel.Empty() {
		return nil, nil, nil
	}
	reqs, selectable := sel.Requirements()
	if !selectable {
		// labels.Nothing(): matches no object.
		return []string{"false"}, nil, nil
	}

	paramIdx := startParam
	for _, req := range reqs {
		clause, reqArgs, err := labelRequirementToSQL(req, paramIdx)
		if err != nil {
			return nil, nil, err
		}
		clauses = append(clauses, clause)
		args = append(args, reqArgs...)
		paramIdx += len(reqArgs)
	}
	return clauses, args, nil
}

func labelRequirementToSQL(req labels.Requirement, p int) (string, []any, error) {
	key := req.Key()
	value := fmt.Sprintf("%s->>$%d", labelsCol, p)

	switch req.Operator() {
	case selection.Equals, selection.DoubleEquals:
		v, _ := req.Values().PopAny()
		contains, err := json.Marshal(map[string]string{key: v})
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("%s @> $%d::jsonb", labelsCol, p), []any{string(contains)}, nil
	case selection.NotEquals:
		v, _ := req.Values().PopAny()
		return fmt.Sprintf("%s IS DISTINCT FROM $%d", value, p+1), []any{key, v}, nil
	case selection.In:
		return fmt.Sprintf("%s = ANY($%d::text[])", value, p+1), []any{key, req.Values().List()}, nil
	case selection.NotIn:
		return fmt.Sprintf("(%s IS NULL OR NOT %s = ANY($%d::text[]))", value, value, p+1),
			[]any{key, req.Values().List()}, nil
	case selection.Exists:
		return fmt.Sprintf("%s ? $%d", labelsCol, p), []any{key}, nil
	case selection.DoesNotExist:
		return fmt.Sprintf("NOT COALESCE(%s ? $%d, false)", labelsCol, p), []any{key}, nil
	case selection.GreaterThan, selection.LessThan:
		v, _ := req.Values().PopAny()
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return "", nil, fmt.Errorf("pgruntime: label selector %q: value %q is not an integer", key, v)
		}
		op := ">"
		if req.Operator() == selection.LessThan {
			op = "<"
		}
		// Non-integer label values never match, as in labels.Requirement.Matches.
		return fmt.Sprintf("(CASE WHEN %s ~ '^-?[0-9]{1,18}$' THEN (%s)::bigint %s $%d ELSE false END)",
			value, value, op, p+1), []any{key, n}, nil
	default:
		return "", nil, fmt.Errorf("pgruntime: unsupported label selector operator %q", req.Operator())
	}
}

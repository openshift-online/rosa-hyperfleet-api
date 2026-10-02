package hyperfleetdb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

// buildLabelSelectorFilter translates a Kubernetes label selector into SQL
// predicates over metadata->'labels'. Positive value matches use JSONB
// containment so PostgreSQL can use the labels GIN index.
func buildLabelSelectorFilter(sel labels.Selector, startParam int) (clauses []string, args []any, err error) {
	if sel == nil || sel.Empty() {
		return nil, nil, nil
	}

	requirements, selectable := sel.Requirements()
	if !selectable {
		return []string{"FALSE"}, nil, nil
	}

	paramIdx := startParam
	for _, req := range requirements {
		key := req.Key()
		values := req.Values().List()

		switch req.Operator() {
		case selection.Equals, selection.DoubleEquals, selection.In:
			if len(values) == 0 {
				return nil, nil, fmt.Errorf("pgruntime: label selector %q requires at least one value", req.String())
			}
			var matches []string
			for _, value := range values {
				clause, arg, err := labelValueMatch(key, value, paramIdx)
				if err != nil {
					return nil, nil, err
				}
				matches = append(matches, clause)
				args = append(args, arg)
				paramIdx++
			}
			match := strings.Join(matches, " OR ")
			if len(matches) > 1 {
				match = "(" + match + ")"
			}
			clauses = append(clauses, match)

		case selection.NotEquals, selection.NotIn:
			var matches []string
			for _, value := range values {
				clause, arg, err := labelValueMatch(key, value, paramIdx)
				if err != nil {
					return nil, nil, err
				}
				matches = append(matches, clause)
				args = append(args, arg)
				paramIdx++
			}
			if len(matches) == 0 {
				clauses = append(clauses, "TRUE")
				continue
			}
			clauses = append(clauses, "NOT COALESCE(("+strings.Join(matches, " OR ")+"), FALSE)")

		case selection.GreaterThan, selection.LessThan:
			if len(values) != 1 {
				return nil, nil, fmt.Errorf("pgruntime: label selector %q requires exactly one integer value", req.String())
			}
			if _, err := strconv.ParseInt(values[0], 10, 64); err != nil {
				return nil, nil, fmt.Errorf("pgruntime: label selector %q requires an int64 value: %w", req.String(), err)
			}
			clause, numericArgs := labelNumericMatch(key, req.Operator(), values[0], paramIdx)
			clauses = append(clauses, clause)
			args = append(args, numericArgs...)
			paramIdx += len(numericArgs)

		case selection.Exists, selection.DoesNotExist:
			clause := "COALESCE((metadata->'labels' ? $%d), FALSE)"
			if req.Operator() == selection.DoesNotExist {
				clause = "NOT " + clause
			}
			clauses = append(clauses, fmt.Sprintf(clause, paramIdx))
			args = append(args, key)
			paramIdx++

		default:
			return nil, nil, fmt.Errorf("pgruntime: unsupported label selector operator %q", req.Operator())
		}
	}

	return clauses, args, nil
}

func labelValueMatch(key, value string, paramIdx int) (string, json.RawMessage, error) {
	encoded, err := json.Marshal(map[string]string{key: value})
	if err != nil {
		return "", nil, fmt.Errorf("pgruntime: encode label selector requirement: %w", err)
	}
	return fmt.Sprintf("(metadata->'labels' @> $%d::jsonb)", paramIdx), json.RawMessage(encoded), nil
}

func labelNumericMatch(key string, op selection.Operator, value string, startParam int) (string, []any) {
	comparison := ">"
	if op == selection.LessThan {
		comparison = "<"
	}

	labelValue := fmt.Sprintf("(metadata->'labels'->>$%d)", startParam)
	clause := fmt.Sprintf(
		"(CASE WHEN %s ~ '^[+-]?[0-9]+$' AND pg_input_is_valid(%s, 'bigint') THEN %s::bigint %s $%d::bigint ELSE FALSE END)",
		labelValue, labelValue, labelValue, comparison, startParam+1,
	)
	return clause, []any{key, value}
}

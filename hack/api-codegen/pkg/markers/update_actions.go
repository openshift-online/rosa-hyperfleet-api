package markers

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// UpdateActionDeclaration supplies an action for a discovered passthrough field.
// It never supplies write modes, visibility, or feature gates.
type UpdateActionDeclaration struct {
	Kind         string `json:"kind"`
	FieldPath    string `json:"fieldPath"`
	UpdateAction string `json:"updateAction"`
}

//go:embed update_actions.json
var updateActionsJSON []byte

// DefaultUpdateActions is the single authoritative passthrough action overlay.
func DefaultUpdateActions() ([]UpdateActionDeclaration, error) {
	var declarations []UpdateActionDeclaration
	if err := json.Unmarshal(updateActionsJSON, &declarations); err != nil {
		return nil, fmt.Errorf("loading update action declarations: %w", err)
	}
	return declarations, nil
}

func updateActionKind(action string) string {
	switch action {
	case "UpdateCluster", "UpdateClusterConfig", "UpdateClusterVersion":
		return "Cluster"
	case "UpdateNodePool", "ScaleNodePool", "UpdateNodePoolVersion":
		return "NodePool"
	default:
		return ""
	}
}

func (s *MarkerScanner) applyUpdateActions() error {
	seen := make(map[string]bool)
	for _, declaration := range s.UpdateActions {
		key := declaration.Kind + "." + declaration.FieldPath
		if seen[key] {
			return fmt.Errorf("duplicate update action declaration for %s", key)
		}
		seen[key] = true
		if s.crdTypes[declaration.Kind] == "" {
			return fmt.Errorf("unknown update action kind %q", declaration.Kind)
		}
		kind := updateActionKind(declaration.UpdateAction)
		if kind == "" {
			return fmt.Errorf("unknown update action %q for %s", declaration.UpdateAction, key)
		}
		if kind != declaration.Kind {
			return fmt.Errorf("update action %q is incompatible with kind %s", declaration.UpdateAction, declaration.Kind)
		}
		meta, ok := s.TypedRegistry[declaration.Kind][declaration.FieldPath]
		if !ok {
			return fmt.Errorf("unknown update action field %s", key)
		}
		if meta.UpdateAction != "" && meta.UpdateAction != declaration.UpdateAction {
			return fmt.Errorf("conflicting update actions for %s: %s and %s", key, meta.UpdateAction, declaration.UpdateAction)
		}
		meta.UpdateAction = declaration.UpdateAction
		s.TypedRegistry[declaration.Kind][declaration.FieldPath] = meta
	}
	return nil
}

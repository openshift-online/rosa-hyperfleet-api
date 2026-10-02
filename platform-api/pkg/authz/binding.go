package authz

import (
	"fmt"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/ast"
)

type bindingMode string

const (
	exactPrincipal bindingMode = "exact-principal"
	roleMembership bindingMode = "role-membership"
)

// bindPolicy adds a principal equality or role-membership condition to a fresh policy without replacing its original restrictions.
func bindPolicy(content string, target cedar.EntityUID, mode bindingMode) (*cedar.Policy, error) {
	if target.ID == "" {
		return nil, fmt.Errorf("binding target ID is empty")
	}

	var constraint ast.Node
	switch mode {
	case exactPrincipal:
		if target.Type != "HyperFleet::Principal" && target.Type != "HyperFleet::Role" {
			return nil, fmt.Errorf("exact binding requires a principal or role entity")
		}
		constraint = ast.Principal().Equal(ast.Value(target))
	case roleMembership:
		if target.Type != "HyperFleet::Role" {
			return nil, fmt.Errorf("membership binding requires a role entity")
		}
		constraint = ast.Principal().In(ast.Value(target))
	default:
		return nil, fmt.Errorf("unsupported binding mode %q", mode)
	}

	// Each attachment owns its AST. Binding narrows rather than replaces scopes.
	var fresh ast.Policy
	if err := fresh.UnmarshalCedar([]byte(content)); err != nil {
		return nil, fmt.Errorf("parse binding policy: %w", err)
	}

	// Reject unrelated principals before evaluating original resource conditions.
	conditions := fresh.Conditions
	fresh.Conditions = nil
	fresh.When(constraint)
	fresh.Conditions = append(fresh.Conditions, conditions...)
	return cedar.NewPolicyFromAST(&fresh), nil
}

package authz

import (
	_ "embed"
	"fmt"

	cedar "github.com/cedar-policy/cedar-go"
	expast "github.com/cedar-policy/cedar-go/x/exp/ast"
	"github.com/cedar-policy/cedar-go/x/exp/schema"
	"github.com/cedar-policy/cedar-go/x/exp/schema/resolved"
	"github.com/cedar-policy/cedar-go/x/exp/schema/validate"
)

//go:embed testdata/hyperfleet.cedarschema
var schemaContent []byte

// loadSchema resolves the embedded Cedar schema and creates its strict validator.
func loadSchema() (*resolved.Schema, *validate.Validator, error) {
	var parsed schema.Schema
	if err := parsed.UnmarshalCedar(schemaContent); err != nil {
		return nil, nil, fmt.Errorf("schema parsing: %w", err)
	}
	model, err := parsed.Resolve()
	if err != nil {
		return nil, nil, fmt.Errorf("schema resolution: %w", err)
	}
	return model, validate.New(model, validate.WithStrict()), nil
}

// entityUID creates an entity identifier in the HyperFleet namespace.
func entityUID(kind, id string) cedar.EntityUID {
	return cedar.NewEntityUID(cedar.EntityType("HyperFleet::"+kind), cedar.String(id))
}

// expPolicy returns a read-only policy AST view for the experimental schema validator.
func expPolicy(policy *cedar.Policy) *expast.Policy {
	return (*expast.Policy)(policy.AST())
}

// checkedPolicy parses exactly one Cedar statement and validates it against the schema.
func checkedPolicy(content, id string, v *validate.Validator) (*cedar.Policy, error) {
	policies, err := cedar.NewPolicyListFromBytes(id, []byte(content))
	if err != nil {
		return nil, err
	}
	if len(policies) != 1 {
		return nil, fmt.Errorf("policy record requires exactly one statement")
	}
	if err := v.Policy(id, expPolicy(policies[0])); err != nil {
		return nil, err
	}
	return policies[0], nil
}

// newFailure associates an internal cause and optional provenance with an authorization failure stage.
func newFailure(stage Stage, err error, provenance ...Provenance) *Failure {
	return &Failure{Stage: stage, Err: err, Provenance: provenance}
}

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

func entityUID(kind, id string) cedar.EntityUID {
	return cedar.NewEntityUID(cedar.EntityType("HyperFleet::"+kind), cedar.String(id))
}

func expPolicy(policy *cedar.Policy) *expast.Policy {
	return (*expast.Policy)(policy.AST())
}

func checkedPolicy(content, id string, v *validate.Validator) error {
	policies, err := cedar.NewPolicyListFromBytes(id, []byte(content))
	if err != nil {
		return err
	}
	if len(policies) != 1 {
		return fmt.Errorf("policy record requires exactly one statement")
	}
	return v.Policy(id, expPolicy(policies[0]))
}

func newFailure(stage Stage, err error, provenance ...Provenance) *Failure {
	return &Failure{Stage: stage, Err: err, Provenance: provenance}
}

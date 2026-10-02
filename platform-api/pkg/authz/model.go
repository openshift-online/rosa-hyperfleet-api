package authz

import (
	"context"

	cedar "github.com/cedar-policy/cedar-go"
)

// Identity contains only trusted gateway identity and service region values.
type Identity struct {
	AccountID string
	CallerARN string
	Region    string
}

type Action string

const (
	ListClusters    Action = "ListClusters"
	DescribeCluster Action = "DescribeCluster"
)

type ResourceKind string

const (
	Collection ResourceKind = "Collection"
	Cluster    ResourceKind = "Cluster"
)

// Resource describes an account-scoped stored object, not request-body metadata.
type Resource struct {
	Kind      ResourceKind
	ID        string
	AccountID string
	Region    string
	Labels    map[string]string
}

type Provenance struct {
	DiagnosticID       string
	PolicyID           string
	PolicyRevision     string
	AttachmentID       string
	AttachmentRevision string
	PrincipalARN       string
	Scope              string
	Region             string
}

type ResolvedBinding struct {
	Provenance
	OwnerAccountID string
	PolicyContent  string
	BindingMode    string
	Caller         Identity
}

// PolicyResolver returns the complete applicable set or an error.
type PolicyResolver interface {
	Resolve(context.Context, Identity) ([]ResolvedBinding, error)
}

type Decision struct {
	Allowed    bool
	Provenance []Provenance
}

type Stage string

const (
	StageNone             Stage = ""
	StageResolution       Stage = "resolution"
	StageParsing          Stage = "parsing"
	StageBinding          Stage = "binding"
	StageEntityValidation Stage = "entity_validation"
	StageEvaluation       Stage = "evaluation"
	StageResourceLoading  Stage = "resource_loading"
)

// Failure keeps sensitive diagnostic material separate from the safe error text.
type Failure struct {
	Stage       Stage
	Provenance  []Provenance
	Diagnostics []cedar.DiagnosticError
	Err         error
}

func (f *Failure) Error() string { return "authorization failed" }
func (f *Failure) Unwrap() error { return f.Err }

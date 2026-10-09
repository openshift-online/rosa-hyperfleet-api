package authz

import (
	"time"

	cedar "github.com/cedar-policy/cedar-go"
)

// Identity contains only trusted gateway identity values.
type Identity struct {
	AccountID string
	CallerARN string
}

type Action string

const (
	ListClusters              Action = "ListClusters"
	DescribeCluster           Action = "DescribeCluster"
	CreateCluster             Action = "CreateCluster"
	UpdateCluster             Action = "UpdateCluster"
	UpdateClusterConfig       Action = "UpdateClusterConfig"
	UpdateClusterVersion      Action = "UpdateClusterVersion"
	DeleteCluster             Action = "DeleteCluster"
	ListNodePools             Action = "ListNodePools"
	DescribeNodePool          Action = "DescribeNodePool"
	CreateNodePool            Action = "CreateNodePool"
	UpdateNodePool            Action = "UpdateNodePool"
	ScaleNodePool             Action = "ScaleNodePool"
	UpdateNodePoolVersion     Action = "UpdateNodePoolVersion"
	DeleteNodePool            Action = "DeleteNodePool"
	ListOIDCConfigs           Action = "ListOIDCConfigs"
	DescribeOIDCConfig        Action = "DescribeOIDCConfig"
	CreateOIDCConfig          Action = "CreateOIDCConfig"
	DeleteOIDCConfig          Action = "DeleteOIDCConfig"
	ListManagementClusters    Action = "ListManagementClusters"
	DescribeManagementCluster Action = "DescribeManagementCluster"
	CreateManagementCluster   Action = "CreateManagementCluster"
)

type ResourceKind string

const (
	Collection        ResourceKind = "Collection"
	Cluster           ResourceKind = "Cluster"
	NodePool          ResourceKind = "NodePool"
	OIDCConfig        ResourceKind = "OIDCConfig"
	ManagementCluster ResourceKind = "ManagementCluster"
	ServiceCollection ResourceKind = "ServiceCollection"
)

type RequestContext struct {
	SourceIP    string
	UserAgent   string
	RequestTime time.Time
}

type ParentCluster struct {
	ID, AccountID string
	Labels        map[string]string
}

// Resource contains trusted stored identity/labels, or a validated create candidate.
type Resource struct {
	Kind               ResourceKind
	ID, AccountID      string
	Labels             map[string]string
	CollectionKind     ResourceKind
	ParentCluster      *ParentCluster
	RegistrationRegion string
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

// Error returns a safe message without exposing the internal cause or diagnostics.
func (f *Failure) Error() string { return "authorization failed" }

// Unwrap exposes the internal cause for error inspection.
func (f *Failure) Unwrap() error { return f.Err }

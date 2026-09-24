package dynamo

import (
	"github.com/openshift-online/rosa-hyperfleet-kube-applier/api/kubeapplier"
)

// DesireConditionSuccessful is the condition type kube-applier-aws sets on
// desire statuses. Aliased from the shared kubeapplier package to avoid drift.
const DesireConditionSuccessful = kubeapplier.ConditionTypeSuccessful

// Type aliases so that code in this package and its callers can continue to
// use the dynamo.Foo names without change. These are true aliases (not new
// types), so values are assignment-compatible with the kubeapplier originals.
type (
	ResourceReference     = kubeapplier.ResourceReference
	DynamoDBMetadata      = kubeapplier.DynamoDBMetadata
	ApplyDesireType       = kubeapplier.ApplyDesireType
	ApplyDesire           = kubeapplier.ApplyDesire
	ApplyDesireSpec       = kubeapplier.ApplyDesireSpec
	ServerSideApplyConfig = kubeapplier.ServerSideApplyConfig
	ApplyDesireStatus     = kubeapplier.ApplyDesireStatus
	ReadDesire            = kubeapplier.ReadDesire
	ReadDesireSpec        = kubeapplier.ReadDesireSpec
	ReadDesireStatus      = kubeapplier.ReadDesireStatus
)

const (
	ApplyDesireTypeServerSideApply = kubeapplier.ApplyDesireTypeServerSideApply
	ApplyDesireTypeDelete          = kubeapplier.ApplyDesireTypeDelete
)

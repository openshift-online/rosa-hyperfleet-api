package silence

import (
	"fmt"
	"strings"
	"time"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

// Intent describes whether a cluster should have a lifecycle silence and why.
type Intent struct {
	Reason Reason
}

// IntentForCluster maps cluster lifecycle phase to silence intent.
// Installing maps to WaitingForPlacement and Provisioning; uninstalling maps to Deleting.
// limited_support and maintenance are deferred until per-state exemption policies land
// (see rosa-hyperfleet docs/design/lifecycle-alert-silencing.md).
func IntentForCluster(cluster *hyperfleetv1alpha1.Cluster) *Intent {
	if cluster == nil {
		return nil
	}
	if !cluster.DeletionTimestamp.IsZero() || cluster.Status.Phase == hyperfleetv1alpha1.ClusterPhaseDeleting {
		return &Intent{Reason: ReasonDeleting}
	}
	switch cluster.Status.Phase {
	case hyperfleetv1alpha1.ClusterPhaseWaitingForPlacement, hyperfleetv1alpha1.ClusterPhaseProvisioning:
		return &Intent{Reason: ReasonInstalling}
	case hyperfleetv1alpha1.ClusterPhaseReady:
		return nil
	default:
		return nil
	}
}

// IdentityFromCluster derives alert label matchers for a cluster CR.
func IdentityFromCluster(cluster *hyperfleetv1alpha1.Cluster) ClusterIdentity {
	return ClusterIdentity{
		Namespace: cluster.Namespace,
		Name:      cluster.Name,
	}
}

// BuildPostableSilence constructs an Alertmanager silence for the given intent.
func BuildPostableSilence(identity ClusterIdentity, reason Reason, now time.Time, ttl time.Duration) PostableSilence {
	matchers := []Matcher{
		{Name: "namespace", Value: identity.Namespace, IsEqual: true},
		{Name: "name", Value: identity.Name, IsEqual: true},
	}
	if reason == ReasonInstalling {
		for _, alert := range InstallExemptAlerts {
			matchers = append(matchers, Matcher{
				Name:    "alertname",
				Value:   alert,
				IsRegex: false,
				IsEqual: false,
			})
		}
	}
	return PostableSilence{
		Matchers:  matchers,
		StartsAt:  now.UTC(),
		EndsAt:    now.Add(ttl).UTC(),
		CreatedBy: CreatedBy,
		Comment:   fmt.Sprintf("lifecycle=%s namespace=%s name=%s", reason, identity.Namespace, identity.Name),
	}
}

// NeedsRenewal reports whether a silence should be replaced before it expires.
func NeedsRenewal(s GettableSilence, now time.Time) bool {
	if s.Status.State != "active" {
		return false
	}
	return s.EndsAt.Sub(now) < RenewBefore
}

// MatchesReason checks whether an existing silence was created for the given lifecycle reason.
func MatchesReason(s GettableSilence, reason Reason) bool {
	want := fmt.Sprintf("lifecycle=%s", reason)
	return strings.Contains(s.Comment, want)
}

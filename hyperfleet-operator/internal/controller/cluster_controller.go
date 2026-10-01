/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/render"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const (
	clusterFinalizer   = "hyperfleet.io/cluster"
	statusRefreshDelay = 5 * time.Minute
	taskKey            = "hyperfleet-operator"
)

// ClusterReconciler reconciles a Cluster object by creating DynamoDB desires
// that kube-applier-aws applies to the management cluster.
type ClusterReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	Dynamo                  dynamo.DesireClient
	RegionalConfig          render.RegionalConfig
	StatusEvents            chan event.GenericEvent
	EventRouter             *EventRouter
	MaxConcurrentReconciles int
}

// +kubebuilder:rbac:groups=hyperfleet.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hyperfleet.io,resources=clusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hyperfleet.io,resources=clusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=hyperfleet.io,resources=nodepools,verbs=get;list;watch
// +kubebuilder:rbac:groups=hyperfleet.io,resources=placements,verbs=get;list;watch
// +kubebuilder:rbac:groups=hyperfleet.io,resources=oidcconfigs,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=hyperfleet.io,resources=indices,verbs=get;list;watch;create;delete

// oidcSigningKeyExternal reports whether cluster's referenced OidcConfig is unmanaged
func (r *ClusterReconciler) oidcSigningKeyExternal(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) (bool, error) {
	if cluster.Spec.OidcConfigID == "" {
		return false, nil
	}
	var oc hyperfleetv1alpha1.OidcConfig
	key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Spec.OidcConfigID}
	if err := r.Get(ctx, key, &oc); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get oidcconfig %s: %w", cluster.Spec.OidcConfigID, err)
	}
	return oc.Spec.Type == hyperfleetv1alpha1.OidcConfigTypeUnmanaged, nil
}

// releaseOidcConfigClaim removes the claimed-by-cluster-uid claim platform-api set on cluster's referenced
// OidcConfig at create time, so the config becomes claimable again by a future cluster. A claim held by
// any other uid is left alone.
func (r *ClusterReconciler) releaseOidcConfigClaim(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) error {
	key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Spec.OidcConfigID}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var oc hyperfleetv1alpha1.OidcConfig
		if err := r.Get(ctx, key, &oc); err != nil {
			return client.IgnoreNotFound(err)
		}
		if oc.Labels[hyperfleetv1alpha1.ClaimedByClusterUIDLabel] != string(cluster.UID) {
			return nil
		}
		delete(oc.Labels, hyperfleetv1alpha1.ClaimedByClusterUIDLabel)
		return r.Update(ctx, &oc)
	})
}

func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cluster hyperfleetv1alpha1.Cluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion via standard Kubernetes DeletionTimestamp.
	if !cluster.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &cluster)
	}

	// Ensure finalizer.
	if !controllerutil.ContainsFinalizer(&cluster, clusterFinalizer) {
		controllerutil.AddFinalizer(&cluster, clusterFinalizer)
		if err := r.Update(ctx, &cluster); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		// The finalizer update emits a watch event that re-enqueues this object.
		return ctrl.Result{}, nil
	}

	if expired, err := r.deleteIfExpired(ctx, &cluster); expired {
		return ctrl.Result{}, err
	}

	// Look up Placement — if none or not Bound, wait.
	placement, err := getPlacement(ctx, r, &cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Waiting for Placement", "cluster", cluster.Name)
			r.setPhase(ctx, &cluster, hyperfleetv1alpha1.ClusterPhaseWaitingForPlacement)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get placement: %w", err)
	}
	if placement.Status.Phase != hyperfleetv1alpha1.PlacementPhaseBound {
		log.Info("Placement not yet Bound", "placement", placement.Name)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Reserve a DNS base domain before rendering resources.
	baseDomain := cluster.Status.BaseDomain
	if baseDomain == "" {
		var err error
		baseDomain, err = r.reserveDNS(ctx, &cluster)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	mc := placement.Spec.ManagementCluster
	// Record the MC on the cluster before anything is rendered to it: once the
	// cluster is deleted, the garbage collector may remove the Placement before
	// the finalizer runs, and the finalizer must still know where to clean up.
	if err := r.recordPlacement(ctx, &cluster, placement); err != nil {
		return ctrl.Result{}, err
	}
	specsPrefix := dynamo.SpecsPrefix(mc)
	statusPrefix := dynamo.StatusPrefix(mc)

	// Render resources and build common structures used by both paths.
	oidcSigningKeyExternal, err := r.oidcSigningKeyExternal(ctx, &cluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve oidc signing key mode: %w", err)
	}
	resources, err := render.ClusterResources(&cluster, oidcSigningKeyExternal, baseDomain)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("render cluster resources: %w", err)
	}

	clusterID := string(cluster.UID)
	hcName := cluster.Name
	hcNs := hyperfleetv1alpha1.ManagementClusterNamespace(cluster.UID)
	readDocID := dynamo.NewDocumentID(taskKey+"-read", "hypershift.openshift.io", "v1beta1", "hostedclusters", hcNs, hcName)

	// Upsert ApplyDesires in parallel — no-op when content matches.
	type upsertResult struct {
		entry DesireStatusEntry
		err   error
	}
	upsertResults := make([]upsertResult, len(resources))
	var upsertWg sync.WaitGroup
	for i, m := range resources {
		upsertWg.Add(1)
		go func(idx int, m render.Resource) {
			defer upsertWg.Done()
			docID := dynamo.NewDocumentID(taskKey, m.Group, m.Version, m.Resource, m.Namespace, m.Name)
			content, marshalErr := json.Marshal(m.Object)
			if marshalErr != nil {
				upsertResults[idx] = upsertResult{err: fmt.Errorf("marshal resource %s: %w", m.Name, marshalErr)}
				return
			}
			desire := &dynamo.ApplyDesire{
				DynamoDBMetadata: dynamo.DynamoDBMetadata{DocumentID: docID},
				Spec: dynamo.ApplyDesireSpec{
					Type:              dynamo.ApplyDesireTypeServerSideApply,
					ManagementCluster: mc,
					ClusterID:         clusterID,
					TargetItem: dynamo.ResourceReference{
						Group:     m.Group,
						Version:   m.Version,
						Resource:  m.Resource,
						Namespace: m.Namespace,
						Name:      m.Name,
					},
					ServerSideApply: &dynamo.ServerSideApplyConfig{
						KubeContent: &runtime.RawExtension{Raw: content},
					},
				},
			}
			res, upsertErr := r.Dynamo.UpsertApplyDesire(ctx, specsPrefix, desire)
			if upsertErr != nil {
				upsertResults[idx] = upsertResult{err: fmt.Errorf("upsert apply desire %s: %w", m.Name, upsertErr)}
				return
			}
			upsertResults[idx] = upsertResult{entry: DesireStatusEntry{DocID: docID, Resource: m.Resource, Name: m.Name, DesireUpdateTime: res.UpdateTime}}
			if r.EventRouter != nil {
				r.EventRouter.Register(docID, EventTarget{Channel: r.StatusEvents, Key: req.NamespacedName})
			}
		}(i, m)
	}

	// Upsert ReadDesire concurrently with ApplyDesires.
	var readErr error
	upsertWg.Go(func() {
		readDesire := &dynamo.ReadDesire{
			DynamoDBMetadata: dynamo.DynamoDBMetadata{DocumentID: readDocID},
			Spec: dynamo.ReadDesireSpec{
				ManagementCluster: mc,
				ClusterID:         clusterID,
				TargetItem: dynamo.ResourceReference{
					Group:     "hypershift.openshift.io",
					Version:   "v1beta1",
					Resource:  "hostedclusters",
					Namespace: hcNs,
					Name:      hcName,
				},
			},
		}
		if _, err := r.Dynamo.UpsertReadDesire(ctx, specsPrefix, readDesire); err != nil {
			readErr = fmt.Errorf("upsert read desire: %w", err)
			return
		}
		if r.EventRouter != nil {
			r.EventRouter.Register(readDocID, EventTarget{Channel: r.StatusEvents, Key: req.NamespacedName})
		}
	})
	upsertWg.Wait()

	if readErr != nil {
		return ctrl.Result{}, readErr
	}
	var applyEntries []DesireStatusEntry
	for _, ur := range upsertResults {
		if ur.err != nil {
			return ctrl.Result{}, ur.err
		}
		applyEntries = append(applyEntries, ur.entry)
	}

	// Read status feedback from DynamoDB and update Cluster status.
	// Phase transitions (Provisioning, Ready) are handled inside updateStatusFromDynamo
	// to avoid clobbering Ready with a stale in-memory phase check.
	r.updateStatusFromDynamo(ctx, &cluster, statusPrefix, readDocID, applyEntries)

	requeueAfter := statusRefreshDelay
	if cluster.Spec.ExpirationTimestamp != nil && !cluster.Spec.ExpirationTimestamp.IsZero() {
		if remaining := time.Until(cluster.Spec.ExpirationTimestamp.Time); remaining > 0 && remaining < requeueAfter {
			requeueAfter = remaining
		}
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *ClusterReconciler) reconcileDelete(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(cluster, clusterFinalizer) {
		return ctrl.Result{}, nil
	}

	log.Info("Cluster deleting", "cluster", cluster.Name)
	r.setPhase(ctx, cluster, hyperfleetv1alpha1.ClusterPhaseDeleting)

	// Resolve the management cluster. If none is set, no resources were ever
	// placed, so skip straight to Placement/finalizer cleanup.
	mc := ""
	if cluster.Status.PlacementRef != nil {
		mc = cluster.Status.PlacementRef.ManagementCluster
	} else if placement, err := getPlacement(ctx, r, cluster); err == nil {
		mc = placement.Spec.ManagementCluster
	}
	if mc == "" {
		return r.cleanupAndRemoveFinalizer(ctx, cluster)
	}

	specsPrefix := dynamo.SpecsPrefix(mc)
	statusPrefix := dynamo.StatusPrefix(mc)
	ns := hyperfleetv1alpha1.ManagementClusterNamespace(cluster.UID)
	hcName := cluster.Name
	clusterID := string(cluster.UID)

	baseDomain := cluster.Status.BaseDomain

	// Render the cluster resources — must match the original resource set so
	// every ApplyDesire (including the OIDC signing key ExternalSecret, if
	// any) gets flipped to Delete below instead of left dangling.
	oidcSigningKeyExternal, err := r.oidcSigningKeyExternal(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve oidc signing key mode: %w", err)
	}
	resources, err := render.ClusterResources(cluster, oidcSigningKeyExternal, baseDomain)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("render cluster resources: %w", err)
	}

	// Switch all 7 ApplyDesires to Type=Delete in-place, using the same
	// taskKey and documentID as the original SSA desires. kube-applier
	// sees a MODIFY stream event per resource and deletes each one from
	// the MC instead of re-applying it.
	type upsertResult struct {
		entry DesireStatusEntry
		err   error
	}
	upsertResults := make([]upsertResult, len(resources))
	var wg sync.WaitGroup
	for i, m := range resources {
		wg.Add(1)
		go func(idx int, m render.Resource) {
			defer wg.Done()
			docID := dynamo.NewDocumentID(taskKey, m.Group, m.Version, m.Resource, m.Namespace, m.Name)
			desire := &dynamo.ApplyDesire{
				DynamoDBMetadata: dynamo.DynamoDBMetadata{DocumentID: docID},
				Spec: dynamo.ApplyDesireSpec{
					Type:              dynamo.ApplyDesireTypeDelete,
					ManagementCluster: mc,
					ClusterID:         clusterID,
					TargetItem: dynamo.ResourceReference{
						Group:     m.Group,
						Version:   m.Version,
						Resource:  m.Resource,
						Namespace: m.Namespace,
						Name:      m.Name,
					},
				},
			}
			res, upsertErr := r.Dynamo.UpsertApplyDesire(ctx, specsPrefix, desire)
			upsertResults[idx] = upsertResult{
				entry: DesireStatusEntry{
					DocID:            docID,
					Resource:         m.Resource,
					Name:             m.Name,
					DesireUpdateTime: res.UpdateTime,
				},
				err: upsertErr,
			}
		}(i, m)
	}
	wg.Wait()

	var deleteEntries []DesireStatusEntry
	for _, ur := range upsertResults {
		if ur.err != nil {
			return ctrl.Result{}, fmt.Errorf("upsert delete desire for %s: %w", ur.entry.Name, ur.err)
		}
		deleteEntries = append(deleteEntries, ur.entry)
	}

	// Wait for all 7 resources to be confirmed deleted by kube-applier.
	syncedCond := CheckApplyDesireStatuses(ctx, r.Dynamo, statusPrefix, deleteEntries, cluster.Generation)
	r.setSyncedCondition(ctx, cluster, syncedCond)
	if syncedCond.Status != metav1.ConditionTrue {
		log.Info("Waiting for cluster resources to be deleted on management cluster")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Wait for the cluster's children (NodePools, Placement) to be gone. The
	// garbage collector deletes them because this cluster is being deleted,
	// and each NodePool's finalizer tears down its HyperShift NodePool.
	if pending, err := countClusterOwned(ctx, r.Client, r.Scheme, cluster); err != nil {
		return ctrl.Result{}, err
	} else if pending > 0 {
		log.Info("Waiting for owned objects to be garbage collected", "count", pending)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// All MC resources deleted — clean up desire specs from DynamoDB.
	readDocID := dynamo.NewDocumentID(taskKey+"-read", "hypershift.openshift.io", "v1beta1", "hostedclusters", ns, hcName)
	if err := r.Dynamo.DeleteDesireSpec(ctx, specsPrefix, "-readdesires", readDocID); err != nil {
		log.Error(err, "failed to clean up ReadDesire spec", "hostedcluster", hcName)
	}
	for _, m := range resources {
		docID := dynamo.NewDocumentID(taskKey, m.Group, m.Version, m.Resource, m.Namespace, m.Name)
		if err := r.Dynamo.DeleteDesireSpec(ctx, specsPrefix, "-applydesires", docID); err != nil {
			log.Error(err, "failed to clean up ApplyDesire spec", "resource", m.Name)
		}
	}

	return r.cleanupAndRemoveFinalizer(ctx, cluster)
}

func (r *ClusterReconciler) cleanupAndRemoveFinalizer(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Release the 1:1 binding claim this cluster took on its referenced OidcConfig, freeing it for reuse; retries on conflict since a missed release permanently orphans the config.
	if cluster.Spec.OidcConfigID != "" {
		if err := r.releaseOidcConfigClaim(ctx, cluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("release oidc config claim: %w", err)
		}
	}

	// Release the cluster's Index claims (its DNS prefix). Only Indexes
	// carrying this cluster's uid are deleted.
	if err := releaseIndexes(ctx, r.Client, cluster.UID); err != nil {
		return ctrl.Result{}, fmt.Errorf("release index claims: %w", err)
	}

	// Nothing may carry this cluster's uid when it goes: a child inserted
	// after an earlier check is collected by the garbage collector, and this
	// finalizer waits for it.
	if pending, err := countClusterOwned(ctx, r.Client, r.Scheme, cluster); err != nil {
		return ctrl.Result{}, err
	} else if pending > 0 {
		log.Info("Waiting for owned objects to be garbage collected", "count", pending)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.Cluster
		if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), &latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !controllerutil.ContainsFinalizer(&latest, clusterFinalizer) {
			return nil
		}
		controllerutil.RemoveFinalizer(&latest, clusterFinalizer)
		return r.Update(ctx, &latest)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

// recordPlacement sets cluster.status.placementRef from placement if it is not
// already recorded.
func (r *ClusterReconciler) recordPlacement(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster, placement *hyperfleetv1alpha1.Placement) error {
	if ref := cluster.Status.PlacementRef; ref != nil && ref.Name == placement.Name && ref.ManagementCluster == placement.Spec.ManagementCluster {
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.Cluster
		if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), &latest); err != nil {
			return err
		}
		latest.Status.PlacementRef = &hyperfleetv1alpha1.PlacementReference{
			Name:              placement.Name,
			ManagementCluster: placement.Spec.ManagementCluster,
		}
		if err := r.Status().Update(ctx, &latest); err != nil {
			return err
		}
		cluster.Status.PlacementRef = latest.Status.PlacementRef
		return nil
	})
}

func (r *ClusterReconciler) updateStatusFromDynamo(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster, statusPrefix, readDocID string, applyEntries []DesireStatusEntry) {
	log := logf.FromContext(ctx)

	// Read HC status and check apply desire statuses in parallel.
	var readStatus *dynamo.ReadDesireStatus
	var readErr error
	var syncedCond metav1.Condition
	var wg sync.WaitGroup

	wg.Go(func() {
		readStatus, readErr = r.Dynamo.GetReadDesireStatus(ctx, statusPrefix, readDocID)
	})

	if len(applyEntries) > 0 {
		wg.Go(func() {
			syncedCond = CheckApplyDesireStatuses(ctx, r.Dynamo, statusPrefix, applyEntries, cluster.Generation)
		})
	}
	wg.Wait()

	if readErr != nil {
		log.V(1).Info("ReadDesire status not yet available", "error", readErr)
	}

	var hc struct {
		Status struct {
			Conditions []metav1.Condition `json:"conditions"`
			Version    struct {
				History []struct {
					Version string `json:"version"`
				} `json:"history"`
			} `json:"version"`
			ControlPlaneEndpoint hypershiftv1beta1.APIEndpoint `json:"controlPlaneEndpoint"`
		} `json:"status"`
	}
	if readStatus != nil && readStatus.KubeContent != nil {
		if err := json.Unmarshal(readStatus.KubeContent.Raw, &hc); err != nil {
			log.Error(err, "Failed to unmarshal HostedCluster status")
		}
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.Cluster
		if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		if len(applyEntries) > 0 {
			meta.SetStatusCondition(&latest.Status.Conditions, syncedCond)
		}

		if readStatus != nil && readStatus.KubeContent != nil {
			for _, cond := range hc.Status.Conditions {
				if cond.Type == "Available" || cond.Type == "Degraded" {
					meta.SetStatusCondition(&latest.Status.Conditions, cond)
				}
			}
			if hc.Status.ControlPlaneEndpoint.Host != "" {
				latest.Status.ControlPlaneEndpoint = hc.Status.ControlPlaneEndpoint
			}
			if len(hc.Status.Version.History) > 0 {
				latest.Status.Version = hc.Status.Version.History[0].Version
			}
		}

		if meta.IsStatusConditionTrue(latest.Status.Conditions, "Available") &&
			!meta.IsStatusConditionTrue(latest.Status.Conditions, "Degraded") {
			latest.Status.Phase = hyperfleetv1alpha1.ClusterPhaseReady
		} else if latest.Status.Phase == "" || latest.Status.Phase == hyperfleetv1alpha1.ClusterPhaseWaitingForPlacement {
			latest.Status.Phase = hyperfleetv1alpha1.ClusterPhaseProvisioning
		}
		latest.Status.ObservedGeneration = latest.Generation
		return r.Status().Update(ctx, &latest)
	}); err != nil {
		log.Error(err, "Failed to update cluster status from DynamoDB feedback")
	}
}

func (r *ClusterReconciler) setSyncedCondition(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster, cond metav1.Condition) {
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.Cluster
		if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		meta.SetStatusCondition(&latest.Status.Conditions, cond)
		return r.Status().Update(ctx, &latest)
	}); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update Synced condition")
	}
}

func (r *ClusterReconciler) deleteIfExpired(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) (bool, error) {
	if cluster.Spec.ExpirationTimestamp == nil || cluster.Spec.ExpirationTimestamp.IsZero() ||
		!time.Now().After(cluster.Spec.ExpirationTimestamp.Time) {
		return false, nil
	}
	logf.FromContext(ctx).Info("Cluster expired, triggering deletion", "expirationTimestamp", cluster.Spec.ExpirationTimestamp.Time)
	if err := r.Delete(ctx, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return true, fmt.Errorf("delete expired cluster: %w", err)
	}
	return true, nil
}

func (r *ClusterReconciler) setPhase(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster, phase hyperfleetv1alpha1.ClusterPhase) {
	if cluster.Status.Phase == phase {
		return
	}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.Cluster
		if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if latest.Status.Phase == phase {
			return nil
		}
		latest.Status.Phase = phase
		latest.Status.ObservedGeneration = latest.Generation
		return r.Status().Update(ctx, &latest)
	}); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update cluster phase", "phase", phase)
	}
}

// defaultDNSShard is the single DNS zone shard used today. When multi-shard
// routing is needed, this will be replaced by dynamic shard selection.
const defaultDNSShard = "0"

const (
	dnsShardNSPrefix = "dns-shard-"
	dnsShardNSSuffix = "-reservations"
)

// dnsShardNamespace is the Index uniqueness scope for DNS prefixes in shard.
func dnsShardNamespace(shard string) string {
	return dnsShardNSPrefix + shard + dnsShardNSSuffix
}

// dnsShardFromNamespace returns the shard whose uniqueness scope is ns.
func dnsShardFromNamespace(ns string) (string, bool) {
	shard, ok := strings.CutPrefix(ns, dnsShardNSPrefix)
	if !ok {
		return "", false
	}
	return strings.CutSuffix(shard, dnsShardNSSuffix)
}

func (r *ClusterReconciler) dnsBaseDomain(prefix, shard string) string {
	return fmt.Sprintf("%s.%s.%s", prefix, shard, r.RegionalConfig.BaseDomainSuffix)
}

// reserveDNS claims a DNS prefix for cluster with a single Index in the shard's
// uniqueness scope, records the assembled base domain in cluster.status.baseDomain,
// and returns it. The Index is only the lock; the data lives on the cluster.
func (r *ClusterReconciler) reserveDNS(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) (string, error) {
	// A claim from an earlier attempt whose status update failed.
	held, err := heldIndexes(ctx, r, cluster.UID)
	if err != nil {
		return "", err
	}
	for i := range held {
		if shard, ok := dnsShardFromNamespace(held[i].Namespace); ok {
			baseDomain := r.dnsBaseDomain(held[i].Name, shard)
			return baseDomain, r.persistBaseDomain(ctx, cluster, baseDomain)
		}
	}

	shard := defaultDNSShard
	for range 5 {
		prefix, err := randomHex4()
		if err != nil {
			return "", fmt.Errorf("generate dns prefix: %w", err)
		}

		claimed, err := claimIndex(ctx, r.Client, dnsShardNamespace(shard), prefix, cluster.UID)
		if err != nil {
			return "", fmt.Errorf("claim dns prefix: %w", err)
		}
		if !claimed {
			continue // another cluster holds this prefix
		}

		baseDomain := r.dnsBaseDomain(prefix, shard)
		if err := r.persistBaseDomain(ctx, cluster, baseDomain); err != nil {
			return "", err
		}
		return baseDomain, nil
	}

	return "", fmt.Errorf("failed to reserve a DNS prefix for %s after 5 attempts", cluster.Name)
}

func (r *ClusterReconciler) persistBaseDomain(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster, baseDomain string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.Cluster
		if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), &latest); err != nil {
			return err
		}
		latest.Status.BaseDomain = baseDomain
		return r.Status().Update(ctx, &latest)
	})
}

func randomHex4() (string, error) {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		For(&hyperfleetv1alpha1.Cluster{}).
		Owns(&hyperfleetv1alpha1.Placement{}).
		Named("cluster")

	if r.StatusEvents != nil {
		b = b.WatchesRawSource(source.Channel(
			r.StatusEvents,
			handler.EnqueueRequestsFromMapFunc(
				func(_ context.Context, obj client.Object) []reconcile.Request {
					return []reconcile.Request{{
						NamespacedName: types.NamespacedName{
							Namespace: obj.GetNamespace(),
							Name:      obj.GetName(),
						},
					}}
				},
			),
		))
	}

	return b.Complete(r)
}

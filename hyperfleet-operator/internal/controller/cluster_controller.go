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
	"encoding/json"
	"fmt"
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

	accountIDLabel  = "hyperfleet.io/account-id"
	accountNSPrefix = "account-"
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
// +kubebuilder:rbac:groups=hyperfleet.io,resources=nodepools,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=hyperfleet.io,resources=placements,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=hyperfleet.io,resources=oidcconfigs,verbs=get;list;watch

// oidcSigningKeyExternal reports whether cluster's referenced OidcConfig is unmanaged
func (r *ClusterReconciler) oidcSigningKeyExternal(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) (bool, error) {
	if cluster.Spec.OidcConfigID == "" {
		return false, nil
	}
	accountID := cluster.Labels[accountIDLabel]
	if accountID == "" {
		return false, nil
	}
	var oc hyperfleetv1alpha1.OidcConfig
	key := types.NamespacedName{Namespace: accountNSPrefix + accountID, Name: cluster.Spec.OidcConfigID}
	if err := r.Get(ctx, key, &oc); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get oidcconfig %s: %w", cluster.Spec.OidcConfigID, err)
	}
	return oc.Spec.Type == hyperfleetv1alpha1.OidcConfigTypeUnmanaged, nil
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

	// Delete the cluster if it has expired.
	if cluster.Spec.ExpirationTimestamp != nil && !cluster.Spec.ExpirationTimestamp.IsZero() &&
		time.Now().After(cluster.Spec.ExpirationTimestamp.Time) {
		log.Info("Cluster expired, triggering deletion", "expirationTimestamp", cluster.Spec.ExpirationTimestamp.Time)
		if err := r.Delete(ctx, &cluster); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("delete expired cluster: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Look up Placement — if none or not Bound, wait.
	placementName := fmt.Sprintf("%s-placement", cluster.Name)
	var placement hyperfleetv1alpha1.Placement
	if err := r.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: placementName}, &placement); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Waiting for Placement", "cluster", cluster.Name)
			r.setPhase(ctx, &cluster, hyperfleetv1alpha1.ClusterPhaseWaitingForPlacement)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get placement: %w", err)
	}
	if placement.Status.Phase != hyperfleetv1alpha1.PlacementPhaseBound {
		log.Info("Placement not yet Bound", "placement", placementName)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	mc := placement.Spec.ManagementCluster
	specsPrefix := dynamo.SpecsPrefix(mc)
	statusPrefix := dynamo.StatusPrefix(mc)

	// Render resources and build common structures used by both paths.
	oidcSigningKeyExternal, err := r.oidcSigningKeyExternal(ctx, &cluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve oidc signing key mode: %w", err)
	}
	resources, err := render.ClusterResources(&cluster, oidcSigningKeyExternal, r.RegionalConfig)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("render cluster resources: %w", err)
	}

	clusterID := render.ClusterIDFromNamespace(cluster.Namespace)
	clusterName := cluster.Name // human-readable

	hcName := clusterName
	hcNs := cluster.Namespace
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
	} else {
		placementName := fmt.Sprintf("%s-placement", cluster.Name)
		var placement hyperfleetv1alpha1.Placement
		if err := r.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: placementName}, &placement); err == nil {
			mc = placement.Spec.ManagementCluster
		}
	}
	if mc == "" {
		return r.cleanupAndRemoveFinalizer(ctx, cluster)
	}

	// Delete NodePool CRs so HyperShift tears down worker nodes.
	var nodePools hyperfleetv1alpha1.NodePoolList
	if err := r.List(ctx, &nodePools, client.InNamespace(cluster.Namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("list nodepools: %w", err)
	}
	pendingNodePools := 0
	for i := range nodePools.Items {
		np := &nodePools.Items[i]
		if np.DeletionTimestamp.IsZero() {
			log.Info("Deleting NodePool", "nodePool", np.Name)
			if err := r.Delete(ctx, np); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("delete nodepool %s: %w", np.Name, err)
			}
		}
		pendingNodePools++
	}

	specsPrefix := dynamo.SpecsPrefix(mc)
	statusPrefix := dynamo.StatusPrefix(mc)
	ns := cluster.Namespace
	hcName := cluster.Name
	clusterID := render.ClusterIDFromNamespace(cluster.Namespace)

	// Render the cluster resources — must match the original resource set so
	// every ApplyDesire (including the OIDC signing key ExternalSecret, if
	// any) gets flipped to Delete below instead of left dangling.
	oidcSigningKeyExternal, err := r.oidcSigningKeyExternal(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve oidc signing key mode: %w", err)
	}
	resources, err := render.ClusterResources(cluster, oidcSigningKeyExternal, r.RegionalConfig)
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

	// Wait for NodePool CRs to be fully removed.
	if pendingNodePools > 0 {
		log.Info("Waiting for NodePools to be deleted", "count", pendingNodePools)
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

	placementName := fmt.Sprintf("%s-placement", cluster.Name)
	var placement hyperfleetv1alpha1.Placement
	if err := r.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: placementName}, &placement); err == nil {
		log.Info("Deleting Placement", "placement", placementName)
		if err := r.Delete(ctx, &placement); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete placement: %w", err)
		}
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

func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		For(&hyperfleetv1alpha1.Cluster{}).
		Watches(&hyperfleetv1alpha1.Placement{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				placement, ok := obj.(*hyperfleetv1alpha1.Placement)
				if !ok {
					return nil
				}
				if placement.Spec.ClusterName == "" {
					return nil
				}
				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Namespace: placement.Namespace,
						Name:      placement.Spec.ClusterName,
					}},
				}
			},
		)).
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

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
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo"
)

const (
	manifestFinalizer          = "hyperfleet.io/manifest"
	manifestTaskKey            = "hyperfleet-manifest"
	manifestStatusRefreshDelay = 5 * time.Minute
)

// ManifestReconciler reconciles Manifest objects by creating
// DynamoDB desires that kube-applier-aws applies to the target management cluster.
// Unlike ClusterReconciler and NodePoolReconciler which generate typed manifests,
// this controller accepts arbitrary Kubernetes resources as raw JSON, enabling
// infrastructure-level resources (ZOA) to be deployed to MCs without new controller code.
type ManifestReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	Dynamo                  dynamo.DesireClient
	StatusEvents            chan event.GenericEvent
	EventRouter             *EventRouter
	MaxConcurrentReconciles int
}

// +kubebuilder:rbac:groups=hyperfleet.io,resources=manifests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hyperfleet.io,resources=manifests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hyperfleet.io,resources=manifests/finalizers,verbs=update

func (r *ManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var hfm hyperfleetv1alpha1.Manifest
	if err := r.Get(ctx, req.NamespacedName, &hfm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !hfm.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &hfm)
	}

	if !controllerutil.ContainsFinalizer(&hfm, manifestFinalizer) {
		controllerutil.AddFinalizer(&hfm, manifestFinalizer)
		if err := r.Update(ctx, &hfm); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		// The finalizer update emits a watch event that re-enqueues this object.
		return ctrl.Result{}, nil
	}

	mc := hfm.Spec.ManagementCluster
	specsPrefix := dynamo.SpecsPrefix(mc)
	scopedTaskKey := manifestScopedTaskKey(&hfm)

	// Build set of current resource document IDs to detect orphans,
	// and entries for Synced condition checking.
	currentDocIDs := make(map[string]struct{}, len(hfm.Spec.Resources))
	var applyEntries []DesireStatusEntry

	type applyItem struct {
		desire   *dynamo.ApplyDesire
		docID    string
		resource string
		name     string
	}
	var applyItems []applyItem

	for _, res := range hfm.Spec.Resources {
		group, version, name, namespace, err := extractResourceMeta(res.Content.Raw)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("extract metadata from resource %s: %w", res.Resource, err)
		}

		docID := dynamo.NewDocumentID(scopedTaskKey, group, version, res.Resource, namespace, name)
		currentDocIDs[docID] = struct{}{}

		desire := &dynamo.ApplyDesire{
			DynamoDBMetadata: dynamo.DynamoDBMetadata{DocumentID: docID},
			Spec: dynamo.ApplyDesireSpec{
				Type:              dynamo.ApplyDesireTypeServerSideApply,
				ManagementCluster: mc,
				ClusterID:         hfm.Name,
				TargetItem: dynamo.ResourceReference{
					Group:     group,
					Version:   version,
					Resource:  res.Resource,
					Namespace: namespace,
					Name:      name,
				},
				ServerSideApply: &dynamo.ServerSideApplyConfig{
					KubeContent: &res.Content,
				},
			},
		}
		applyItems = append(applyItems, applyItem{desire: desire, docID: docID, resource: res.Resource, name: name})

		if r.EventRouter != nil {
			r.EventRouter.Register(docID, EventTarget{Channel: r.StatusEvents, Key: types.NamespacedName{Namespace: hfm.Namespace, Name: hfm.Name}})
		}
	}

	type applyUpsertResult struct {
		updateTime time.Time
		err        error
	}
	applyResults := make([]applyUpsertResult, len(applyItems))
	var wg sync.WaitGroup
	for i, item := range applyItems {
		wg.Add(1)
		go func(idx int, desire *dynamo.ApplyDesire) {
			defer wg.Done()
			ur, err := r.Dynamo.UpsertApplyDesire(ctx, specsPrefix, desire)
			if err != nil {
				applyResults[idx] = applyUpsertResult{err: err}
			} else {
				applyResults[idx] = applyUpsertResult{updateTime: ur.UpdateTime}
			}
		}(i, item.desire)
	}
	wg.Wait()

	for i, item := range applyItems {
		if applyResults[i].err != nil {
			return ctrl.Result{}, fmt.Errorf("upsert apply desire %s/%s: %w", item.resource, item.name, applyResults[i].err)
		}
		applyEntries = append(applyEntries, DesireStatusEntry{DocID: item.docID, Resource: item.resource, Name: item.name, DesireUpdateTime: applyResults[i].updateTime})
	}

	// H1: Clean up orphaned ApplyDesire specs from resources removed since last generation.
	r.cleanupOrphanedDesires(ctx, &hfm, specsPrefix, scopedTaskKey, currentDocIDs)

	log.Info("ApplyDesires written", "count", len(hfm.Spec.Resources), "mc", mc)

	// Write ReadDesires for watched resources.
	type readItem struct {
		desire   *dynamo.ReadDesire
		resource string
		name     string
	}
	var readItems []readItem

	for _, res := range hfm.Spec.Resources {
		if !res.Watch {
			continue
		}
		group, version, name, namespace, err := extractResourceMeta(res.Content.Raw)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("extract metadata for ReadDesire %s: %w", res.Resource, err)
		}
		readDocID := dynamo.NewDocumentID(scopedTaskKey+"-read", group, version, res.Resource, namespace, name)
		readDesire := &dynamo.ReadDesire{
			DynamoDBMetadata: dynamo.DynamoDBMetadata{DocumentID: readDocID},
			Spec: dynamo.ReadDesireSpec{
				ManagementCluster: mc,
				ClusterID:         hfm.Name,
				TargetItem: dynamo.ResourceReference{
					Group:     group,
					Version:   version,
					Resource:  res.Resource,
					Namespace: namespace,
					Name:      name,
				},
			},
		}
		readItems = append(readItems, readItem{desire: readDesire, resource: res.Resource, name: name})
		if r.EventRouter != nil {
			r.EventRouter.Register(readDocID, EventTarget{Channel: r.StatusEvents, Key: types.NamespacedName{Namespace: hfm.Namespace, Name: hfm.Name}})
		}
	}

	hasWatched := len(readItems) > 0
	readUpsertErrs := make([]error, len(readItems))
	wg = sync.WaitGroup{}
	for i, item := range readItems {
		wg.Add(1)
		go func(idx int, desire *dynamo.ReadDesire) {
			defer wg.Done()
			_, readUpsertErrs[idx] = r.Dynamo.UpsertReadDesire(ctx, specsPrefix, desire)
		}(i, item.desire)
	}
	wg.Wait()

	for i, item := range readItems {
		if readUpsertErrs[i] != nil {
			return ctrl.Result{}, fmt.Errorf("upsert read desire %s/%s: %w", item.resource, item.name, readUpsertErrs[i])
		}
	}

	// Poll ReadDesire status to collect resourceStatuses.
	statusPrefix := dynamo.StatusPrefix(mc)
	var resourceStatuses []hyperfleetv1alpha1.ResourceStatus
	if hasWatched {
		resourceStatuses = r.collectResourceStatuses(ctx, &hfm, statusPrefix, scopedTaskKey)
	}

	// Set Synced condition, phase, and resourceStatuses in a single status write
	// to avoid a stale-cache read between two writes erasing resourceStatuses.
	syncedCond := CheckApplyDesireStatuses(ctx, r.Dynamo, statusPrefix, applyEntries, hfm.Generation)

	meta.SetStatusCondition(&hfm.Status.Conditions, syncedCond)
	if syncedCond.Status == metav1.ConditionTrue {
		hfm.Status.Phase = hyperfleetv1alpha1.ManifestPhaseApplied
	} else {
		hfm.Status.Phase = hyperfleetv1alpha1.ManifestPhaseSyncing
	}
	hfm.Status.AppliedResources = int32(len(hfm.Spec.Resources))
	hfm.Status.ObservedGeneration = hfm.Generation
	if len(resourceStatuses) > 0 {
		hfm.Status.ResourceStatuses = resourceStatuses
	}
	if err := r.Status().Update(ctx, &hfm); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}

	if syncedCond.Status != metav1.ConditionTrue {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	if hasWatched {
		return ctrl.Result{RequeueAfter: manifestStatusRefreshDelay}, nil
	}
	return ctrl.Result{}, nil
}

func (r *ManifestReconciler) reconcileDelete(ctx context.Context, hfm *hyperfleetv1alpha1.Manifest) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(hfm, manifestFinalizer) {
		return ctrl.Result{}, nil
	}

	log.Info("Manifest deleting", "manifest", hfm.Name)
	r.setPhase(ctx, hfm, hyperfleetv1alpha1.ManifestPhaseDeleting)

	mc := hfm.Spec.ManagementCluster
	specsPrefix := dynamo.SpecsPrefix(mc)
	statusPrefix := dynamo.StatusPrefix(mc)
	taskKey := manifestScopedTaskKey(hfm)

	// Switch all ApplyDesires to Type=Delete in-place, using the same
	// documentID as the original SSA desires. kube-applier sees a MODIFY
	// stream event per resource and deletes each one from the MC instead of
	// re-applying it.
	type desireEntry struct {
		resource, name string
		docID          string
		updateTime     time.Time
	}
	var entries []desireEntry

	for _, res := range hfm.Spec.Resources {
		group, version, name, namespace, err := extractResourceMeta(res.Content.Raw)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("extract metadata from resource %s: %w", res.Resource, err)
		}

		docID := dynamo.NewDocumentID(taskKey, group, version, res.Resource, namespace, name)
		deleteDesire := &dynamo.ApplyDesire{
			DynamoDBMetadata: dynamo.DynamoDBMetadata{DocumentID: docID},
			Spec: dynamo.ApplyDesireSpec{
				Type:              dynamo.ApplyDesireTypeDelete,
				ManagementCluster: mc,
				ClusterID:         hfm.Name,
				TargetItem: dynamo.ResourceReference{
					Group:     group,
					Version:   version,
					Resource:  res.Resource,
					Namespace: namespace,
					Name:      name,
				},
			},
		}
		ur, upsertErr := r.Dynamo.UpsertApplyDesire(ctx, specsPrefix, deleteDesire)
		if upsertErr != nil {
			return ctrl.Result{}, fmt.Errorf("upsert delete desire %s/%s: %w", res.Resource, name, upsertErr)
		}
		entries = append(entries, desireEntry{resource: res.Resource, name: name, docID: docID, updateTime: ur.UpdateTime})
	}

	// Wait for all resources to be confirmed deleted by kube-applier
	// (Successful=True on each desire's status).
	var statusEntries []DesireStatusEntry
	for _, e := range entries {
		statusEntries = append(statusEntries, DesireStatusEntry{
			DocID:            e.docID,
			Resource:         e.resource,
			Name:             e.name,
			DesireUpdateTime: e.updateTime,
		})
	}
	syncedCond := CheckApplyDesireStatuses(ctx, r.Dynamo, statusPrefix, statusEntries, hfm.Generation)
	r.setSyncedCondition(ctx, hfm, syncedCond)
	if syncedCond.Status != metav1.ConditionTrue {
		log.Info("Waiting for manifest resources to be deleted on management cluster")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// All resources confirmed deleted — remove the ApplyDesire specs from
	// DynamoDB so kube-applier stops tracking them.
	for _, e := range entries {
		if err := r.Dynamo.DeleteDesireSpec(ctx, specsPrefix, "-applydesires", e.docID); err != nil {
			log.Error(err, "Failed to clean up ApplyDesire spec", "resource", e.resource, "name", e.name)
		}
	}

	// Clean up ReadDesire specs for watched resources.
	readTaskKey := taskKey + "-read"
	for _, res := range hfm.Spec.Resources {
		if !res.Watch {
			continue
		}
		group, version, name, namespace, err := extractResourceMeta(res.Content.Raw)
		if err != nil {
			log.Error(err, "Failed to extract metadata for ReadDesire cleanup, skipping", "resource", res.Resource)
			continue
		}
		readDocID := dynamo.NewDocumentID(readTaskKey, group, version, res.Resource, namespace, name)
		if r.EventRouter != nil {
			r.EventRouter.Deregister(readDocID)
		}
		if err := r.Dynamo.DeleteDesireSpec(ctx, specsPrefix, "-readdesires", readDocID); err != nil {
			log.Error(err, "Failed to clean up ReadDesire spec", "resource", res.Resource, "name", name)
		}
	}

	controllerutil.RemoveFinalizer(hfm, manifestFinalizer)
	if err := r.Update(ctx, hfm); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

// cleanupOrphanedDesires removes ApplyDesire specs that exist in DynamoDB but are no
// longer in the current spec. This handles the case where resources are removed from
// spec.resources between generations — without cleanup, the old desires would persist
// indefinitely and the removed resources would remain on the management cluster.
func (r *ManifestReconciler) cleanupOrphanedDesires(ctx context.Context, hfm *hyperfleetv1alpha1.Manifest, specsPrefix, scopedTaskKey string, currentDocIDs map[string]struct{}) {
	log := logf.FromContext(ctx)

	// Only check for orphans when the spec has actually changed.
	if hfm.Generation == hfm.Status.ObservedGeneration {
		return
	}

	// Check each previously-applied resource against current spec.
	// ResourceStatuses tracks what was previously applied — any entry whose
	// docID is absent from currentDocIDs is an orphan.
	for _, rs := range hfm.Status.ResourceStatuses {
		docID := dynamo.NewDocumentID(scopedTaskKey, rs.Group, rs.Version, rs.Resource, rs.Namespace, rs.Name)
		if _, ok := currentDocIDs[docID]; ok {
			continue
		}
		if err := r.Dynamo.DeleteDesireSpec(ctx, specsPrefix, "-applydesires", docID); err != nil {
			if !errors.Is(err, dynamo.ErrNotFound) {
				log.Error(err, "failed to clean up orphaned ApplyDesire", "resource", rs.Resource, "name", rs.Name)
			}
		} else {
			log.Info("Cleaned up orphaned ApplyDesire", "resource", rs.Resource, "name", rs.Name)
		}
		// ResourceStatuses only contains previously-watched resources, so
		// each orphan also has a ReadDesire that needs cleanup.
		readDocID := dynamo.NewDocumentID(scopedTaskKey+"-read", rs.Group, rs.Version, rs.Resource, rs.Namespace, rs.Name)
		if r.EventRouter != nil {
			r.EventRouter.Deregister(readDocID)
		}
		if err := r.Dynamo.DeleteDesireSpec(ctx, specsPrefix, "-readdesires", readDocID); err != nil {
			if !errors.Is(err, dynamo.ErrNotFound) {
				log.Error(err, "failed to clean up orphaned ReadDesire", "resource", rs.Resource, "name", rs.Name)
			}
		}
	}
}

func (r *ManifestReconciler) collectResourceStatuses(ctx context.Context, hfm *hyperfleetv1alpha1.Manifest, statusPrefix, scopedTaskKey string) []hyperfleetv1alpha1.ResourceStatus {
	log := logf.FromContext(ctx)

	type watchedResource struct {
		group, version, resource, name, namespace string
		readDocID                                 string
	}
	var watched []watchedResource
	for _, res := range hfm.Spec.Resources {
		if !res.Watch {
			continue
		}
		group, version, name, namespace, err := extractResourceMeta(res.Content.Raw)
		if err != nil {
			log.Error(err, "failed to extract metadata for resource status collection, skipping", "resource", res.Resource)
			continue
		}
		readDocID := dynamo.NewDocumentID(scopedTaskKey+"-read", group, version, res.Resource, namespace, name)
		watched = append(watched, watchedResource{group: group, version: version, resource: res.Resource, name: name, namespace: namespace, readDocID: readDocID})
	}

	type readResult struct {
		status *dynamo.ReadDesireStatus
		err    error
	}
	results := make([]readResult, len(watched))
	var wg sync.WaitGroup
	for i, w := range watched {
		wg.Add(1)
		go func(idx int, docID string) {
			defer wg.Done()
			s, err := r.Dynamo.GetReadDesireStatus(ctx, statusPrefix, docID)
			results[idx] = readResult{status: s, err: err}
		}(i, w.readDocID)
	}
	wg.Wait()

	var statuses []hyperfleetv1alpha1.ResourceStatus
	for i, w := range watched {
		if results[i].err != nil {
			log.V(1).Info("ReadDesire status not yet available", "resource", w.resource, "name", w.name)
			continue
		}
		if results[i].status.KubeContent == nil {
			continue
		}
		var obj struct {
			Status json.RawMessage `json:"status"`
		}
		if err := json.Unmarshal(results[i].status.KubeContent.Raw, &obj); err != nil {
			log.Error(err, "failed to unmarshal KubeContent", "resource", w.resource, "name", w.name)
			continue
		}
		if len(obj.Status) == 0 {
			continue
		}
		statuses = append(statuses, hyperfleetv1alpha1.ResourceStatus{
			Resource:  w.resource,
			Name:      w.name,
			Namespace: w.namespace,
			Group:     w.group,
			Version:   w.version,
			Status:    runtime.RawExtension{Raw: obj.Status},
		})
	}

	return statuses
}

func (r *ManifestReconciler) setSyncedCondition(ctx context.Context, hfm *hyperfleetv1alpha1.Manifest, cond metav1.Condition) {
	meta.SetStatusCondition(&hfm.Status.Conditions, cond)
	if err := r.Status().Update(ctx, hfm); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update Synced condition")
	}
}

func (r *ManifestReconciler) setPhase(ctx context.Context, hfm *hyperfleetv1alpha1.Manifest, phase hyperfleetv1alpha1.ManifestPhase) {
	if hfm.Status.Phase == phase {
		return
	}
	hfm.Status.Phase = phase
	hfm.Status.ObservedGeneration = hfm.Generation
	if err := r.Status().Update(ctx, hfm); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update manifest phase", "phase", phase)
	}
}

func (r *ManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		For(&hyperfleetv1alpha1.Manifest{}).
		WatchesRawSource(source.Channel(r.StatusEvents, &handler.EnqueueRequestForObject{})).
		Named("manifest").
		Complete(r)
}

// manifestScopedTaskKey returns a taskKey scoped to the CR's identity,
// preventing two Manifest CRs deploying the same resource
// from producing colliding DynamoDB document IDs.
func manifestScopedTaskKey(hfm *hyperfleetv1alpha1.Manifest) string {
	return fmt.Sprintf("%s/%s/%s", manifestTaskKey, hfm.Namespace, hfm.Name)
}

// extractResourceMeta parses apiVersion, metadata.name, and metadata.namespace
// from a raw Kubernetes resource manifest.
func extractResourceMeta(raw []byte) (group, version, name, namespace string, err error) {
	var m struct {
		APIVersion string `json:"apiVersion"`
		Metadata   struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", "", "", "", fmt.Errorf("unmarshal content: %w", err)
	}
	if m.Metadata.Name == "" {
		return "", "", "", "", fmt.Errorf("content missing metadata.name")
	}
	if m.APIVersion == "" {
		return "", "", "", "", fmt.Errorf("content missing apiVersion")
	}
	parts := strings.SplitN(m.APIVersion, "/", 2)
	if len(parts) == 1 {
		return "", parts[0], m.Metadata.Name, m.Metadata.Namespace, nil
	}
	return parts[0], parts[1], m.Metadata.Name, m.Metadata.Namespace, nil
}

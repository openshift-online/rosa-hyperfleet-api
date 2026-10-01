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
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

// ClusterOwnedKinds are the kinds a Cluster owns through a controller
// ownerReference and the hyperfleet.io/cluster-uid label. The garbage collector
// is registered for each, and a cluster's finalizer waits until none carries
// its uid. A new owned kind must be added here.
func ClusterOwnedKinds() []client.Object {
	return []client.Object{&hyperfleetv1alpha1.NodePool{}, &hyperfleetv1alpha1.Placement{}}
}

// GarbageCollector deletes objects of one owned kind whose controller owner is
// missing, has a different uid (the name was reused), or is being deleted.
// Deleting only sets the object's deletion timestamp; its own finalizer still
// does its teardown. hyperfleet-db has no built-in garbage collection, so this
// is what makes cleanup race-free: a child inserted after its owner's
// finalizer found no children is still collected.
type GarbageCollector struct {
	client.Client
	Scheme *runtime.Scheme
	// Owned is the kind collected, e.g. &NodePool{}.
	Owned                   client.Object
	MaxConcurrentReconciles int
}

// +kubebuilder:rbac:groups=hyperfleet.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=hyperfleet.io,resources=nodepools,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=hyperfleet.io,resources=placements,verbs=get;list;watch;delete

func (r *GarbageCollector) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj, ok := r.Owned.DeepCopyObject().(client.Object)
	if !ok {
		return ctrl.Result{}, fmt.Errorf("owned kind %T is not a client.Object", r.Owned)
	}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	ref := metav1.GetControllerOf(obj)
	if ref == nil {
		return ctrl.Result{}, nil
	}
	reason, err := r.orphanReason(ctx, obj.GetNamespace(), ref)
	if err != nil || reason == "" {
		return ctrl.Result{}, err
	}

	logf.FromContext(ctx).Info("Deleting object whose owner is gone", "reason", reason,
		"owner", ref.Kind+"/"+ref.Name, "ownerUID", ref.UID)
	// The object carries its resourceVersion, so a concurrent change surfaces as
	// a conflict and the object is reconsidered on the next event.
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("delete orphan: %w", err)
	}
	return ctrl.Result{}, nil
}

// orphanReason returns why the owner named in ref no longer owns an object in
// namespace, or "" while it still does.
func (r *GarbageCollector) orphanReason(ctx context.Context, namespace string, ref *metav1.OwnerReference) (string, error) {
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return "", fmt.Errorf("parse owner apiVersion %q: %w", ref.APIVersion, err)
	}
	newOwner, err := r.Scheme.New(gv.WithKind(ref.Kind))
	if err != nil {
		return "", fmt.Errorf("owner kind %s: %w", ref.Kind, err)
	}
	owner, ok := newOwner.(client.Object)
	if !ok {
		return "", fmt.Errorf("owner kind %s is not a client.Object", ref.Kind)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, owner); err != nil {
		if apierrors.IsNotFound(err) {
			return "owner missing", nil
		}
		return "", fmt.Errorf("get owner: %w", err)
	}
	switch {
	case owner.GetUID() != ref.UID:
		return "owner name reused by a different uid", nil
	case !owner.GetDeletionTimestamp().IsZero():
		return "owner being deleted", nil
	}
	return "", nil
}

// SetupWithManager watches the owned kind and its Cluster owners. An owner
// event enqueues the objects carrying its uid label, so deleting an owner
// collects its children promptly; the initial list on start covers the rest.
func (r *GarbageCollector) SetupWithManager(mgr ctrl.Manager) error {
	gvk, err := mgr.GetClient().GroupVersionKindFor(r.Owned)
	if err != nil {
		return fmt.Errorf("garbage collector kind: %w", err)
	}
	listGVK := gvk.GroupVersion().WithKind(gvk.Kind + "List")

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		For(r.Owned).
		Watches(&hyperfleetv1alpha1.Cluster{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, owner client.Object) []reconcile.Request {
				return r.ownedBy(ctx, listGVK, owner)
			},
		)).
		Named("gc-" + strings.ToLower(gvk.Kind)).
		Complete(r)
}

// ownedBy lists the objects of the owned kind that carry owner's uid label.
func (r *GarbageCollector) ownedBy(ctx context.Context, listGVK schema.GroupVersionKind, owner client.Object) []reconcile.Request {
	newList, err := r.Scheme.New(listGVK)
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to build owned list", "kind", listGVK.Kind)
		return nil
	}
	list, ok := newList.(client.ObjectList)
	if !ok {
		return nil
	}
	if err := r.List(ctx, list,
		client.InNamespace(owner.GetNamespace()),
		client.MatchingLabels{hyperfleetv1alpha1.ClusterUIDLabel: string(owner.GetUID())},
	); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list owned objects", "kind", listGVK.Kind)
		return nil
	}
	items, err := metaItems(list)
	if err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(items))
	for _, item := range items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(item)})
	}
	return reqs
}

// countClusterOwned returns how many objects of the cluster-owned kinds still
// carry the cluster's uid label.
func countClusterOwned(ctx context.Context, c client.Client, scheme *runtime.Scheme, cluster *hyperfleetv1alpha1.Cluster) (int, error) {
	total := 0
	for _, owned := range ClusterOwnedKinds() {
		gvk, err := c.GroupVersionKindFor(owned)
		if err != nil {
			return 0, err
		}
		newList, err := scheme.New(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
		if err != nil {
			return 0, err
		}
		list, ok := newList.(client.ObjectList)
		if !ok {
			return 0, fmt.Errorf("%s list is not a client.ObjectList", gvk.Kind)
		}
		if err := c.List(ctx, list, client.MatchingLabels{hyperfleetv1alpha1.ClusterUIDLabel: string(cluster.UID)}); err != nil {
			return 0, fmt.Errorf("list %s: %w", gvk.Kind, err)
		}
		items, err := metaItems(list)
		if err != nil {
			return 0, err
		}
		total += len(items)
	}
	return total, nil
}

func metaItems(list client.ObjectList) ([]client.Object, error) {
	objs, err := meta.ExtractList(list)
	if err != nil {
		return nil, err
	}
	out := make([]client.Object, 0, len(objs))
	for _, o := range objs {
		if co, ok := o.(client.Object); ok {
			out = append(out, co)
		}
	}
	return out, nil
}

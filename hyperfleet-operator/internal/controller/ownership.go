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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

// placementChild is the child part of a cluster's Placement name: "<cluster>.placement".
const placementChild = "placement"

var (
	clusterGR   = hyperfleetv1alpha1.GroupVersion.WithResource("clusters").GroupResource()
	placementGR = hyperfleetv1alpha1.GroupVersion.WithResource("placements").GroupResource()
)

func placementName(cluster *hyperfleetv1alpha1.Cluster) string {
	return hyperfleetv1alpha1.ChildName(cluster.Name, placementChild)
}

// getPlacement returns cluster's Placement. A Placement under the expected name
// that is controlled by a different uid belongs to an earlier cluster that used
// the same name, so it is reported as NotFound.
func getPlacement(ctx context.Context, c client.Reader, cluster *hyperfleetv1alpha1.Cluster) (*hyperfleetv1alpha1.Placement, error) {
	var placement hyperfleetv1alpha1.Placement
	key := types.NamespacedName{Namespace: cluster.Namespace, Name: placementName(cluster)}
	if err := c.Get(ctx, key, &placement); err != nil {
		return nil, err
	}
	if !metav1.IsControlledBy(&placement, cluster) {
		return nil, apierrors.NewNotFound(placementGR, key.Name)
	}
	return &placement, nil
}

// getOwnerCluster returns the Cluster that controls obj, found by the name in its
// controller ownerReference and checked by uid. It returns NotFound when the
// cluster is gone or its name now belongs to a different cluster.
func getOwnerCluster(ctx context.Context, c client.Reader, obj client.Object) (*hyperfleetv1alpha1.Cluster, error) {
	ref := metav1.GetControllerOf(obj)
	if ref == nil || ref.Kind != "Cluster" {
		return nil, apierrors.NewNotFound(clusterGR, "")
	}
	var cluster hyperfleetv1alpha1.Cluster
	if err := c.Get(ctx, types.NamespacedName{Namespace: obj.GetNamespace(), Name: ref.Name}, &cluster); err != nil {
		return nil, err
	}
	if cluster.UID != ref.UID {
		return nil, apierrors.NewNotFound(clusterGR, ref.Name)
	}
	return &cluster, nil
}

// claimIndex claims the value name in the uniqueness scope namespace for owner
// by creating an Index. It reports whether owner holds the claim: true when the
// Index was created or already carries owner's uid (so retries are safe), false
// when another owner holds it.
func claimIndex(ctx context.Context, c client.Client, namespace, name string, owner types.UID) (bool, error) {
	idx := &hyperfleetv1alpha1.Index{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    map[string]string{hyperfleetv1alpha1.OwnerUIDLabel: string(owner)},
		},
	}
	err := c.Create(ctx, idx)
	if err == nil {
		return true, nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return false, fmt.Errorf("create index %s/%s: %w", namespace, name, err)
	}
	var existing hyperfleetv1alpha1.Index
	if err := c.Get(ctx, client.ObjectKeyFromObject(idx), &existing); err != nil {
		// NotFound here means the holder just released it; the caller retries.
		return false, fmt.Errorf("get index %s/%s: %w", namespace, name, err)
	}
	return existing.Labels[hyperfleetv1alpha1.OwnerUIDLabel] == string(owner), nil
}

// heldIndexes lists the Indexes owner holds, in any scope.
func heldIndexes(ctx context.Context, c client.Reader, owner types.UID) ([]hyperfleetv1alpha1.Index, error) {
	var list hyperfleetv1alpha1.IndexList
	if err := c.List(ctx, &list, client.MatchingLabels{hyperfleetv1alpha1.OwnerUIDLabel: string(owner)}); err != nil {
		return nil, fmt.Errorf("list indexes held by %s: %w", owner, err)
	}
	return list.Items, nil
}

// releaseIndexes deletes every Index owner holds, and never one held by anyone else.
func releaseIndexes(ctx context.Context, c client.Client, owner types.UID) error {
	held, err := heldIndexes(ctx, c, owner)
	if err != nil {
		return err
	}
	for i := range held {
		if err := c.Delete(ctx, &held[i]); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete index %s/%s: %w", held[i].Namespace, held[i].Name, err)
		}
	}
	return nil
}

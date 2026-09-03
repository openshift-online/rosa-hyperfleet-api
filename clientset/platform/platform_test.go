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

package platform

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"

	typedclient "github.com/openshift-online/rosa-hyperfleet-api/clientset/generated/typed/v1alpha1/public"
)

func encodeTestCursor(t *testing.T, txidStamp uint64, accountID string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"txid_stamp": txidStamp, "account_id": accountID})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// stubClusterClient is a minimal implementation of typedclient.ClusterInterface.
// Methods that should not be called during validation tests panic to catch regressions.
type stubClusterClient struct {
	getFunc func(ctx context.Context, name string, opts metav1.GetOptions) (*v1alpha1.Cluster, error)
}

func (s *stubClusterClient) Get(ctx context.Context, name string, opts metav1.GetOptions) (*v1alpha1.Cluster, error) {
	if s.getFunc != nil {
		return s.getFunc(ctx, name, opts)
	}
	panic("stubClusterClient.Get called unexpectedly")
}
func (s *stubClusterClient) List(ctx context.Context, opts metav1.ListOptions) (*v1alpha1.ClusterList, error) {
	panic("stubClusterClient.List called unexpectedly")
}
func (s *stubClusterClient) Create(ctx context.Context, obj *v1alpha1.Cluster, opts metav1.CreateOptions) (*v1alpha1.Cluster, error) {
	panic("stubClusterClient.Create called unexpectedly")
}
func (s *stubClusterClient) Update(ctx context.Context, obj *v1alpha1.Cluster, opts metav1.UpdateOptions) (*v1alpha1.Cluster, error) {
	panic("stubClusterClient.Update called unexpectedly")
}
func (s *stubClusterClient) UpdateStatus(ctx context.Context, obj *v1alpha1.Cluster, opts metav1.UpdateOptions) (*v1alpha1.Cluster, error) {
	panic("stubClusterClient.UpdateStatus called unexpectedly")
}
func (s *stubClusterClient) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	panic("stubClusterClient.Delete called unexpectedly")
}
func (s *stubClusterClient) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	panic("stubClusterClient.DeleteCollection called unexpectedly")
}
func (s *stubClusterClient) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	panic("stubClusterClient.Watch called unexpectedly")
}
func (s *stubClusterClient) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*v1alpha1.Cluster, error) {
	panic("stubClusterClient.Patch called unexpectedly")
}

var _ typedclient.ClusterInterface = (*stubClusterClient)(nil)

// stubNodePoolClient is a minimal implementation of typedclient.NodePoolInterface.
type stubNodePoolClient struct {
	getFunc func(ctx context.Context, name string, opts metav1.GetOptions) (*v1alpha1.NodePool, error)
}

func (s *stubNodePoolClient) Get(ctx context.Context, name string, opts metav1.GetOptions) (*v1alpha1.NodePool, error) {
	if s.getFunc != nil {
		return s.getFunc(ctx, name, opts)
	}
	panic("stubNodePoolClient.Get called unexpectedly")
}
func (s *stubNodePoolClient) List(ctx context.Context, opts metav1.ListOptions) (*v1alpha1.NodePoolList, error) {
	panic("stubNodePoolClient.List called unexpectedly")
}
func (s *stubNodePoolClient) Create(ctx context.Context, obj *v1alpha1.NodePool, opts metav1.CreateOptions) (*v1alpha1.NodePool, error) {
	panic("stubNodePoolClient.Create called unexpectedly")
}
func (s *stubNodePoolClient) Update(ctx context.Context, obj *v1alpha1.NodePool, opts metav1.UpdateOptions) (*v1alpha1.NodePool, error) {
	panic("stubNodePoolClient.Update called unexpectedly")
}
func (s *stubNodePoolClient) UpdateStatus(ctx context.Context, obj *v1alpha1.NodePool, opts metav1.UpdateOptions) (*v1alpha1.NodePool, error) {
	panic("stubNodePoolClient.UpdateStatus called unexpectedly")
}
func (s *stubNodePoolClient) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	panic("stubNodePoolClient.Delete called unexpectedly")
}
func (s *stubNodePoolClient) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	panic("stubNodePoolClient.DeleteCollection called unexpectedly")
}
func (s *stubNodePoolClient) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	panic("stubNodePoolClient.Watch called unexpectedly")
}
func (s *stubNodePoolClient) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*v1alpha1.NodePool, error) {
	panic("stubNodePoolClient.Patch called unexpectedly")
}

var _ typedclient.NodePoolInterface = (*stubNodePoolClient)(nil)

// clusterClient helpers

func newClusterClient(stub *stubClusterClient) *clusterClient {
	return &clusterClient{inner: stub}
}

func newNodePoolClient(stub *stubNodePoolClient) *nodePoolClient {
	return &nodePoolClient{inner: stub}
}

// clusterClient.List validation

func TestClusterList_NegativeLimitRejected(t *testing.T) {
	c := newClusterClient(&stubClusterClient{})
	if _, err := c.List(context.Background(), ListOptions{Limit: -1}); err == nil {
		t.Error("expected error for negative Limit")
	}
}

func TestClusterList_LimitOver100Rejected(t *testing.T) {
	c := newClusterClient(&stubClusterClient{})
	if _, err := c.List(context.Background(), ListOptions{Limit: 101}); err == nil {
		t.Error("expected error for Limit > 100")
	}
}

func TestClusterList_ContinuePassedThrough(t *testing.T) {
	token := encodeTestCursor(t, 42, "123456789012")
	var gotOpts metav1.ListOptions
	stub := &captureListClusterClient{captureFn: func(opts metav1.ListOptions) { gotOpts = opts }}
	c := &clusterClient{inner: stub}
	_, _ = c.List(context.Background(), ListOptions{Continue: token})
	if gotOpts.Continue != token {
		t.Errorf("Continue = %q, want %q", gotOpts.Continue, token)
	}
}

// nodePoolClient.List validation

func TestNodePoolList_NegativeLimitRejected(t *testing.T) {
	c := newNodePoolClient(&stubNodePoolClient{})
	if _, err := c.List(context.Background(), ListOptions{Limit: -1}); err == nil {
		t.Error("expected error for negative Limit")
	}
}

func TestNodePoolList_LimitOver100Rejected(t *testing.T) {
	c := newNodePoolClient(&stubNodePoolClient{})
	if _, err := c.List(context.Background(), ListOptions{Limit: 101}); err == nil {
		t.Error("expected error for Limit > 100")
	}
}

func TestNodePoolList_ContinuePassedThrough(t *testing.T) {
	token := encodeTestCursor(t, 99, "123456789012")
	var gotOpts metav1.ListOptions
	stub := &captureListNodePoolClient{captureFn: func(opts metav1.ListOptions) { gotOpts = opts }}
	c := &nodePoolClient{inner: stub}
	_, _ = c.List(context.Background(), ListOptions{Continue: token})
	if gotOpts.Continue != token {
		t.Errorf("Continue = %q, want %q", gotOpts.Continue, token)
	}
}

// clusterClient.WaitUntil interval validation

func TestClusterWaitUntil_ZeroIntervalRejected(t *testing.T) {
	c := newClusterClient(&stubClusterClient{})
	err := c.WaitUntil(context.Background(), "id", func(*v1alpha1.Cluster) bool { return true }, 0, time.Minute)
	if err == nil {
		t.Error("expected error for zero interval")
	}
}

func TestClusterWaitUntil_NegativeIntervalRejected(t *testing.T) {
	c := newClusterClient(&stubClusterClient{})
	err := c.WaitUntil(context.Background(), "id", func(*v1alpha1.Cluster) bool { return true }, -time.Second, time.Minute)
	if err == nil {
		t.Error("expected error for negative interval")
	}
}

// nodePoolClient.WaitUntil interval validation

func TestNodePoolWaitUntil_ZeroIntervalRejected(t *testing.T) {
	c := newNodePoolClient(&stubNodePoolClient{})
	err := c.WaitUntil(context.Background(), "id", func(*v1alpha1.NodePool) bool { return true }, 0, time.Minute)
	if err == nil {
		t.Error("expected error for zero interval")
	}
}

func TestNodePoolWaitUntil_NegativeIntervalRejected(t *testing.T) {
	c := newNodePoolClient(&stubNodePoolClient{})
	err := c.WaitUntil(context.Background(), "id", func(*v1alpha1.NodePool) bool { return true }, -time.Second, time.Minute)
	if err == nil {
		t.Error("expected error for negative interval")
	}
}

// clusterClient.WaitUntil condition behavior

func TestClusterWaitUntil_ConditionImmediatelyTrue(t *testing.T) {
	stub := &stubClusterClient{
		getFunc: func(_ context.Context, _ string, _ metav1.GetOptions) (*v1alpha1.Cluster, error) {
			return &v1alpha1.Cluster{}, nil
		},
	}
	c := newClusterClient(stub)
	err := c.WaitUntil(context.Background(), "id", func(*v1alpha1.Cluster) bool { return true }, 10*time.Millisecond, time.Second)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClusterWaitUntil_TimeoutWhenConditionNeverTrue(t *testing.T) {
	stub := &stubClusterClient{
		getFunc: func(_ context.Context, _ string, _ metav1.GetOptions) (*v1alpha1.Cluster, error) {
			return &v1alpha1.Cluster{}, nil
		},
	}
	c := newClusterClient(stub)
	err := c.WaitUntil(context.Background(), "id", func(*v1alpha1.Cluster) bool { return false }, 5*time.Millisecond, 20*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error when condition never true")
	}
}

// nodePoolClient.WaitUntil condition behavior

func TestNodePoolWaitUntil_ConditionImmediatelyTrue(t *testing.T) {
	stub := &stubNodePoolClient{
		getFunc: func(_ context.Context, _ string, _ metav1.GetOptions) (*v1alpha1.NodePool, error) {
			return &v1alpha1.NodePool{}, nil
		},
	}
	c := newNodePoolClient(stub)
	err := c.WaitUntil(context.Background(), "id", func(*v1alpha1.NodePool) bool { return true }, 10*time.Millisecond, time.Second)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNodePoolWaitUntil_TimeoutWhenConditionNeverTrue(t *testing.T) {
	stub := &stubNodePoolClient{
		getFunc: func(_ context.Context, _ string, _ metav1.GetOptions) (*v1alpha1.NodePool, error) {
			return &v1alpha1.NodePool{}, nil
		},
	}
	c := newNodePoolClient(stub)
	err := c.WaitUntil(context.Background(), "id", func(*v1alpha1.NodePool) bool { return false }, 5*time.Millisecond, 20*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error when condition never true")
	}
}

// clusterClient.Update routes by UID

func TestClusterUpdate_RoutesByUID(t *testing.T) {
	var gotName string
	stub := &stubClusterClient{}
	stub.getFunc = nil // not used by Update
	innerStub := &updateCaptureClusterClient{nameChan: make(chan string, 1)}
	c := &clusterClient{inner: innerStub}

	obj := &v1alpha1.Cluster{}
	obj.Name = "human-name"
	obj.UID = "uid-abc"

	_, _ = c.Update(context.Background(), obj, UpdateOptions{})
	gotName = <-innerStub.nameChan

	if gotName != "uid-abc" {
		t.Errorf("Update routed to name %q, want uid-abc", gotName)
	}
}

// updateCaptureClusterClient captures the name passed to Update.
type updateCaptureClusterClient struct {
	nameChan chan string
	stubClusterClient
}

func (u *updateCaptureClusterClient) Update(_ context.Context, obj *v1alpha1.Cluster, _ metav1.UpdateOptions) (*v1alpha1.Cluster, error) {
	u.nameChan <- obj.Name
	return obj, nil
}

// nodePoolClient.Update routes by UID

func TestNodePoolUpdate_RoutesByUID(t *testing.T) {
	innerStub := &updateCaptureNodePoolClient{nameChan: make(chan string, 1)}
	c := &nodePoolClient{inner: innerStub}

	obj := &v1alpha1.NodePool{}
	obj.Name = "human-name"
	obj.UID = "uid-xyz"

	_, _ = c.Update(context.Background(), obj, UpdateOptions{})
	gotName := <-innerStub.nameChan

	if gotName != "uid-xyz" {
		t.Errorf("Update routed to name %q, want uid-xyz", gotName)
	}
}

// updateCaptureNodePoolClient captures the name passed to Update.
type updateCaptureNodePoolClient struct {
	nameChan chan string
	stubNodePoolClient
}

func (u *updateCaptureNodePoolClient) Update(_ context.Context, obj *v1alpha1.NodePool, _ metav1.UpdateOptions) (*v1alpha1.NodePool, error) {
	u.nameChan <- obj.Name
	return obj, nil
}

// Update must not mutate the caller's object.

func TestClusterUpdate_DoesNotMutateCallerObject(t *testing.T) {
	innerStub := &updateCaptureClusterClient{nameChan: make(chan string, 1)}
	c := &clusterClient{inner: innerStub}

	obj := &v1alpha1.Cluster{}
	obj.Name = "human-name"
	obj.UID = "uid-abc"

	_, _ = c.Update(context.Background(), obj, UpdateOptions{})
	<-innerStub.nameChan

	if obj.Name != "human-name" {
		t.Errorf("Update mutated caller's object: Name = %q", obj.Name)
	}
}

func TestNodePoolUpdate_DoesNotMutateCallerObject(t *testing.T) {
	innerStub := &updateCaptureNodePoolClient{nameChan: make(chan string, 1)}
	c := &nodePoolClient{inner: innerStub}

	obj := &v1alpha1.NodePool{}
	obj.Name = "human-name"
	obj.UID = "uid-xyz"

	_, _ = c.Update(context.Background(), obj, UpdateOptions{})
	<-innerStub.nameChan

	if obj.Name != "human-name" {
		t.Errorf("Update mutated caller's object: Name = %q", obj.Name)
	}
}

// captureListClusterClient captures metav1.ListOptions passed to List.
type captureListClusterClient struct {
	captureFn func(metav1.ListOptions)
	stubClusterClient
}

func (c *captureListClusterClient) List(_ context.Context, opts metav1.ListOptions) (*v1alpha1.ClusterList, error) {
	if c.captureFn != nil {
		c.captureFn(opts)
	}
	return &v1alpha1.ClusterList{}, nil
}

// captureListNodePoolClient captures metav1.ListOptions passed to List.
type captureListNodePoolClient struct {
	captureFn func(metav1.ListOptions)
	stubNodePoolClient
}

func (c *captureListNodePoolClient) List(_ context.Context, opts metav1.ListOptions) (*v1alpha1.NodePoolList, error) {
	if c.captureFn != nil {
		c.captureFn(opts)
	}
	return &v1alpha1.NodePoolList{}, nil
}

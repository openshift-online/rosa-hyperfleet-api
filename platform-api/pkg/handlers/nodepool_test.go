package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	seaplanev1alpha1 "github.com/openshift-online/rosa-seaplane-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-seaplane-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-seaplane-api/platform-api/pkg/clients/seaplanedb"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func testNodePoolCR(npName, accountID string) *seaplanev1alpha1.NodePool {
	return &seaplanev1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      npName,
			Namespace: "account-" + accountID,
			Labels:    map[string]string{"seaplane.io/account-id": accountID},
		},
	}
}

func newTestNodePoolHandler(t *testing.T, objects ...client.Object) *NodePoolHandler {
	t.Helper()
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	return NewNodePoolHandler(seaplanedb.NewClientFrom(fc, logger), logger)
}

// TestNodePoolHandler_Update_MissingSpec verifies that PUT /nodepools/{id} with
// an absent or empty spec is rejected with 400 before any DB write.
func TestNodePoolHandler_Update_MissingSpec(t *testing.T) {
	np := testNodePoolCR("test-cluster.workers", testAccountID)
	handler := newTestNodePoolHandler(t, np)

	cases := []struct {
		name string
		body []byte
	}{
		{"no spec key", mustMarshal(t, map[string]any{})},
		{"empty spec object", mustMarshal(t, map[string]any{"spec": map[string]any{}})},
		{"whitespace spec", []byte(`{"spec":{ }}`)},
		{"whitespace with newline spec", []byte(`{"spec":{
}}`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/v0/nodepools/test-cluster.workers", bytes.NewReader(tc.body))
			req = req.WithContext(testContext(testAccountID))
			req = mux.SetURLVars(req, map[string]string{"id": "test-cluster.workers"})

			w := httptest.NewRecorder()
			handler.Update(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d body=%s", w.Code, w.Body.String())
			}
			var errResp map[string]any
			if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
				t.Fatalf("response body is not valid JSON: %v", err)
			}
			msg, _ := errResp["message"].(string)
			if msg == "" {
				t.Errorf("expected non-empty error message")
			}
		})
	}
}

func TestNodePoolHandler_Update_AllowsUnchangedServiceManagedManagement(t *testing.T) {
	autoRepair := true
	replicas := int32(2)
	nodePool := testNodePoolCR("my-cluster.workers", testAccountID)
	nodePool.Spec = seaplanev1alpha1.NodePoolSpec{
		AutoRepair: &autoRepair,
		NodePool: seaplanev1alpha1.NodePoolSpecPassthrough{
			ClusterName: "my-cluster",
			Replicas:    &replicas,
			Management: hypershiftv1beta1.NodePoolManagement{
				AutoRepair: true,
			},
		},
	}

	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nodePool).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewNodePoolHandler(seaplanedb.NewClientFrom(fc, logger), logger)
	request := seaplanedb.InternalToPublicNodePool(nodePool)
	updatedReplicas := int32(3)
	request.Spec.NodePool.Replicas = &updatedReplicas

	req := httptest.NewRequest(http.MethodPut, "/api/v0/nodepools/"+nodePool.Name, bytes.NewReader(mustMarshal(t, request)))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": nodePool.Name})
	w := httptest.NewRecorder()

	handler.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated seaplanev1alpha1.NodePool
	if err := fc.Get(context.Background(), types.NamespacedName{Namespace: nodePool.Namespace, Name: nodePool.Name}, &updated); err != nil {
		t.Fatalf("get updated NodePool: %v", err)
	}
	if got := updated.Spec.NodePool.Replicas; got == nil || *got != updatedReplicas {
		t.Errorf("replicas = %v, want %d", got, updatedReplicas)
	}
	if !updated.Spec.NodePool.Management.AutoRepair {
		t.Error("round-trip update changed service-managed management.autoRepair")
	}
}

func TestNodePoolHandler_PatchAllowsReplicaUpdateWithOmittedServiceManagedFields(t *testing.T) {
	const poolName = "my-cluster.workers"
	replicas := int32(2)
	nodePool := testNodePoolCR(poolName, testAccountID)
	nodePool.Spec.NodePool = seaplanev1alpha1.NodePoolSpecPassthrough{
		ClusterName: "my-cluster",
		Platform: hypershiftv1beta1.NodePoolPlatform{
			Type: hypershiftv1beta1.AWSPlatform,
		},
		Replicas: &replicas,
		Management: hypershiftv1beta1.NodePoolManagement{
			UpgradeType: hypershiftv1beta1.UpgradeTypeReplace,
		},
	}
	handler := newTestNodePoolHandler(t, nodePool)
	request := httptest.NewRequest(http.MethodPatch, "/api/v0/nodepools/"+poolName,
		strings.NewReader(`{"spec":{"nodePool":{"replicas":3}}}`))
	request = request.WithContext(testContext(testAccountID))
	request = mux.SetURLVars(request, map[string]string{"id": poolName})
	response := httptest.NewRecorder()

	handler.Update(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("PATCH update status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	updated, err := handler.db.GetNodePool(context.Background(), testAccountID, poolName)
	if err != nil {
		t.Fatalf("get updated NodePool: %v", err)
	}
	if got := updated.Spec.NodePool.Replicas; got == nil || *got != 3 {
		t.Errorf("replicas = %v, want 3", got)
	}
	if updated.Spec.NodePool.Platform.Type != hypershiftv1beta1.AWSPlatform {
		t.Errorf("platform type = %q, want %q", updated.Spec.NodePool.Platform.Type, hypershiftv1beta1.AWSPlatform)
	}
	if updated.Spec.NodePool.Management.UpgradeType != hypershiftv1beta1.UpgradeTypeReplace {
		t.Errorf("management upgrade type = %q, want %q", updated.Spec.NodePool.Management.UpgradeType, hypershiftv1beta1.UpgradeTypeReplace)
	}
}

func TestNodePoolHandler_Update_ChangesNativeResourceTags(t *testing.T) {
	cluster := testClusterCR("cluster-uid", "test-cluster", testAccountID)
	cluster.Spec.Tags = map[string]string{"environment": "test"}
	nodePool := testNodePoolCR("test-cluster.workers", testAccountID)
	nodePool.Labels[clusterUIDLabel] = string(cluster.UID)
	nodePool.Spec.NodePool = seaplanev1alpha1.NodePoolSpecPassthrough{
		ClusterName: cluster.Name,
		Platform: hypershiftv1beta1.NodePoolPlatform{
			Type: hypershiftv1beta1.AWSPlatform,
			AWS: &hypershiftv1beta1.AWSNodePoolPlatform{
				InstanceType: "m5.xlarge",
				ResourceTags: []hypershiftv1beta1.AWSResourceTag{{Key: "owner", Value: "old"}},
			},
		},
	}
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, nodePool).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewNodePoolHandler(seaplanedb.NewClientFrom(fc, logger), logger)
	request := seaplanedb.InternalToPublicNodePool(nodePool)
	request.Spec.NodePool.Platform.AWS.ResourceTags = []hypershiftv1beta1.AWSResourceTag{{Key: "owner", Value: "new"}}

	req := httptest.NewRequest(http.MethodPut, "/api/v0/nodepools/"+nodePool.Name, bytes.NewReader(mustMarshal(t, request)))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": nodePool.Name})
	w := httptest.NewRecorder()

	handler.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response public.NodePool
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if tags := response.Spec.NodePool.Platform.AWS.ResourceTags; len(tags) != 1 || tags[0] != (hypershiftv1beta1.AWSResourceTag{Key: "owner", Value: "new"}) {
		t.Errorf("response resourceTags = %v, want owner=new", tags)
	}

	var stored seaplanev1alpha1.NodePool
	if err := fc.Get(context.Background(), types.NamespacedName{Namespace: "account-" + testAccountID, Name: nodePool.Name}, &stored); err != nil {
		t.Fatalf("get updated NodePool: %v", err)
	}
	if tags := stored.Spec.NodePool.Platform.AWS.ResourceTags; len(tags) != 1 || tags[0] != (hypershiftv1beta1.AWSResourceTag{Key: "owner", Value: "new"}) {
		t.Errorf("stored resourceTags = %v, want owner=new", tags)
	}
}

func TestNodePoolHandler_List_ClusterIDParameterFiltersByParentUID(t *testing.T) {
	clusterUID := "550e8400-e29b-41d4-a716-446655440000"
	matching := testNodePoolCR("my-cluster.workers", testAccountID)
	matching.Labels[clusterUIDLabel] = clusterUID
	nonmatching := testNodePoolCR("other-cluster.workers", testAccountID)
	nonmatching.Labels[clusterUIDLabel] = "550e8400-e29b-41d4-a716-446655440001"
	handler := newTestNodePoolHandler(t, matching, nonmatching)
	req := httptest.NewRequest(http.MethodGet, "/api/v0/nodepools?clusterId="+clusterUID, nil)
	req = req.WithContext(testContext(testAccountID))
	w := httptest.NewRecorder()

	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Total != 1 || len(response.Items) != 1 {
		t.Fatalf("expected only the matching NodePool, got %+v", response)
	}
}

func TestNodePoolHandler_List_InvalidSelector(t *testing.T) {
	const clusterUID = "550e8400-e29b-41d4-a716-446655440000"
	tests := []struct {
		name        string
		query       string
		wantErrCode string
	}{
		{
			name:        "invalid cluster UID",
			query:       "clusterUID=not-a-uuid",
			wantErrCode: ErrNodePoolListInvalidClusterUID.Code,
		},
		{
			name:        "malformed label selector",
			query:       "labelSelector=%3D",
			wantErrCode: ErrNodePoolListInvalidLabelSelector.Code,
		},
		{
			name:        "unsupported label selector key",
			query:       "labelSelector=other%3Dvalue",
			wantErrCode: ErrNodePoolListInvalidLabelSelector.Code,
		},
		{
			name:        "unsupported label selector operator",
			query:       "labelSelector=seaplane.io%2Fcluster-uid!%3D" + clusterUID,
			wantErrCode: ErrNodePoolListInvalidLabelSelector.Code,
		},
		{
			name:        "label selector conflicts with cluster UID",
			query:       "clusterUID=" + clusterUID + "&labelSelector=seaplane.io%2Fcluster-uid%3D550e8400-e29b-41d4-a716-446655440001",
			wantErrCode: ErrNodePoolListInvalidLabelSelector.Code,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newTestNodePoolHandler(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v0/nodepools?"+tt.query, nil)
			req = req.WithContext(testContext(testAccountID))
			w := httptest.NewRecorder()

			handler.List(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if message := decodeErrorMessage(t, w); !strings.Contains(message, tt.wantErrCode) {
				t.Errorf("error message = %q, want code %s", message, tt.wantErrCode)
			}
		})
	}
}

func TestNodePoolHandler_Get_Success(t *testing.T) {
	nodepool := testNodePoolCR("my-cluster.workers", testAccountID)
	handler := newTestNodePoolHandler(t, nodepool)
	req := httptest.NewRequest(http.MethodGet, "/api/v0/nodepools/"+nodepool.Name, nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": nodepool.Name})
	w := httptest.NewRecorder()

	handler.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	metadata, _ := response["metadata"].(map[string]any)
	if got := metadata["name"]; got != nodepool.Name {
		t.Errorf("metadata.name = %v, want %q", got, nodepool.Name)
	}
}

func TestNodePoolHandler_Get_NotFound(t *testing.T) {
	handler := newTestNodePoolHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v0/nodepools/missing.workers", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "missing.workers"})
	w := httptest.NewRecorder()

	handler.Get(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNodePoolHandler_Delete_Success(t *testing.T) {
	nodepool := testNodePoolCR("my-cluster.workers", testAccountID)
	handler := newTestNodePoolHandler(t, nodepool)
	req := httptest.NewRequest(http.MethodDelete, "/api/v0/nodepools/"+nodepool.Name, nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": nodepool.Name})
	w := httptest.NewRecorder()

	handler.Delete(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := response["nodepool_name"]; got != nodepool.Name {
		t.Errorf("nodepool_name = %v, want %q", got, nodepool.Name)
	}
}

func TestNodePoolHandler_Delete_NotFound(t *testing.T) {
	handler := newTestNodePoolHandler(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v0/nodepools/missing.workers", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "missing.workers"})
	w := httptest.NewRecorder()

	handler.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNodePoolHandler_Update_NamespaceMismatch(t *testing.T) {
	nodePool := testNodePoolCR("test-cluster.workers", testAccountID)
	handler := newTestNodePoolHandler(t, nodePool)
	body := mustMarshal(t, map[string]any{
		"metadata": map[string]any{"namespace": "account-999999999999"},
		"spec":     map[string]any{"nodePool": map[string]any{"replicas": 3}},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v0/nodepools/"+nodePool.Name, bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": nodePool.Name})
	w := httptest.NewRecorder()

	handler.Update(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if message := decodeErrorMessage(t, w); !strings.Contains(message, ErrNodePoolUpdateInvalidNamespace.Code) {
		t.Errorf("error message = %q, want code %s", message, ErrNodePoolUpdateInvalidNamespace.Code)
	}
}

// TestNodePoolHandler_Create_MissingMetadata verifies that POST /nodepools with
// missing or malformed metadata fields is rejected before any DB lookup.
func TestNodePoolHandler_Create_MissingMetadata(t *testing.T) {
	handler := newTestNodePoolHandler(t) // no pre-seeded objects needed

	cases := []struct {
		name         string
		body         map[string]any
		wantStatus   int
		wantCodeFrag string // substring of the platform error code in message
	}{
		{
			name:         "missing name",
			body:         map[string]any{"metadata": map[string]any{"namespace": "account-" + testAccountID}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateMissingFields.Code,
		},
		{
			name:         "name is not dotted",
			body:         map[string]any{"metadata": map[string]any{"name": "my-pool", "namespace": "account-" + testAccountID}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateInvalidName.Code,
		},
		{
			name:         "name has too many components",
			body:         map[string]any{"metadata": map[string]any{"name": "cluster.pool.extra", "namespace": "account-" + testAccountID}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateInvalidName.Code,
		},
		{
			name:         "namespace belongs to another account",
			body:         map[string]any{"metadata": map[string]any{"name": "my-cluster.workers", "namespace": "account-999999999999"}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateInvalidNamespace.Code,
		},
		{
			name:         "legacy cluster namespace rejected",
			body:         map[string]any{"metadata": map[string]any{"name": "my-cluster.workers", "namespace": "cluster-550e8400-e29b-41d4-a716-446655440000"}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateInvalidNamespace.Code,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v0/nodepools", bytes.NewReader(mustMarshal(t, tc.body)))
			req = req.WithContext(testContext(testAccountID))

			w := httptest.NewRecorder()
			handler.Create(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("expected %d, got %d body=%s", tc.wantStatus, w.Code, w.Body.String())
			}
			var errResp map[string]any
			if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
				t.Fatalf("response body is not valid JSON: %v", err)
			}
			msg, _ := errResp["message"].(string)
			if msg == "" {
				t.Errorf("expected non-empty error message")
			}
			if tc.wantCodeFrag != "" && !containsCode(msg, tc.wantCodeFrag) {
				t.Errorf("message %q does not contain code %s", msg, tc.wantCodeFrag)
			}
		})
	}
}

func TestNodePoolHandler_Create_ResolvesParentFromDottedName(t *testing.T) {
	scheme := newTestScheme()
	cluster := &seaplanev1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name:      "my-cluster",
		Namespace: "account-" + testAccountID,
		UID:       types.UID("550e8400-e29b-41d4-a716-446655440000"),
	}}
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewNodePoolHandler(seaplanedb.NewClientFrom(fc, logger), logger)
	body := mustMarshal(t, map[string]any{
		"metadata": map[string]any{"name": "my-cluster.workers"},
		"spec": map[string]any{"nodePool": map[string]any{
			"clusterName": "client-supplied-parent",
			"release":     map[string]any{"image": "quay.io/openshift-release-dev/ocp-release:4.17.0"},
			"platform":    map[string]any{"type": "AWS"},
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v0/nodepools", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	w := httptest.NewRecorder()

	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var stored seaplanev1alpha1.NodePool
	if err := fc.Get(context.Background(), types.NamespacedName{Namespace: "account-" + testAccountID, Name: "my-cluster.workers"}, &stored); err != nil {
		t.Fatalf("get created NodePool: %v", err)
	}
	if got := stored.Spec.NodePool.ClusterName; got != "my-cluster" {
		t.Errorf("stored parent ClusterName = %q, want %q", got, "my-cluster")
	}
	if got := stored.Spec.EC2InstanceMetadataHttpTokens; got != seaplanev1alpha1.EC2InstanceMetadataHttpTokensRequired {
		t.Errorf("stored ec2InstanceMetadataHttpTokens = %q, want %q", got, seaplanev1alpha1.EC2InstanceMetadataHttpTokensRequired)
	}
	if got := stored.Labels[clusterUIDLabel]; got != string(cluster.UID) {
		t.Errorf("cluster UID label = %q, want %q", got, cluster.UID)
	}
	owner := metav1.GetControllerOf(&stored)
	if owner == nil || owner.Name != cluster.Name || owner.UID != cluster.UID {
		t.Errorf("controller ownerReference = %+v, want Cluster %s UID %s", owner, cluster.Name, cluster.UID)
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if spec, ok := response["spec"].(map[string]any); ok {
		if _, exists := spec["clusterId"]; exists {
			t.Error("response must not expose spec.clusterId")
		}
		if got := spec["ec2InstanceMetadataHttpTokens"]; got != "required" {
			t.Errorf("response ec2InstanceMetadataHttpTokens = %v, want required", got)
		}
	}
}

func TestNodePoolHandler_Create_AcceptsOptionalEC2InstanceMetadataHttpTokens(t *testing.T) {
	scheme := newTestScheme()
	cluster := &seaplanev1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name:      "my-cluster",
		Namespace: "account-" + testAccountID,
		UID:       types.UID("550e8400-e29b-41d4-a716-446655440000"),
	}}
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewNodePoolHandler(seaplanedb.NewClientFrom(fc, logger), logger)
	body := mustMarshal(t, map[string]any{
		"metadata": map[string]any{"name": "my-cluster.optional-workers"},
		"spec": map[string]any{
			"EC2InstanceMetadataHttpTokens": "optional",
			"nodePool": map[string]any{
				"clusterName": "my-cluster",
				"release":     map[string]any{"image": "quay.io/openshift-release-dev/ocp-release:4.17.0"},
				"platform":    map[string]any{"type": "AWS"},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v0/nodepools", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	w := httptest.NewRecorder()

	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var stored seaplanev1alpha1.NodePool
	if err := fc.Get(context.Background(), types.NamespacedName{Namespace: "account-" + testAccountID, Name: "my-cluster.optional-workers"}, &stored); err != nil {
		t.Fatalf("get created NodePool: %v", err)
	}
	if got := stored.Spec.EC2InstanceMetadataHttpTokens; got != seaplanev1alpha1.EC2InstanceMetadataHttpTokensOptional {
		t.Errorf("stored ec2InstanceMetadataHttpTokens = %q, want %q", got, seaplanev1alpha1.EC2InstanceMetadataHttpTokensOptional)
	}
}

// containsCode reports whether the metav1.Status message contains the given code prefix.
func containsCode(message, code string) bool {
	return len(message) >= len(code) && message[:len(code)] == code
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

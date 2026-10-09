package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

func TestValidateNodePoolReplicas(t *testing.T) {
	negative := int32(-1)
	zero := int32(0)
	positive := int32(3)
	tests := []struct {
		name      string
		spec      *public.NodePoolSpec
		wantError bool
	}{
		{name: "nil spec"},
		{name: "replicas omitted", spec: &public.NodePoolSpec{}},
		{name: "zero replicas", spec: &public.NodePoolSpec{NodePool: public.NodePoolSpecPassthrough{Replicas: &zero}}},
		{name: "positive replicas", spec: &public.NodePoolSpec{NodePool: public.NodePoolSpecPassthrough{Replicas: &positive}}},
		{name: "negative replicas", spec: &public.NodePoolSpec{NodePool: public.NodePoolSpecPassthrough{Replicas: &negative}}, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateNodePoolReplicas(tt.spec)
			if gotError := len(errs) > 0; gotError != tt.wantError {
				t.Fatalf("validateNodePoolReplicas() returned errors %v, wantError=%t", errs, tt.wantError)
			}
			if tt.wantError && (errs[0].Field != "spec.nodePool.replicas" || errs[0].Reason != "must be greater than or equal to 0") {
				t.Fatalf("unexpected validation error: %+v", errs[0])
			}
		})
	}
}

func TestNodePoolHandlerCreateRejectsNegativeReplicas(t *testing.T) {
	const (
		accountID = "123456789012"
		clusterID = "550e8400-e29b-41d4-a716-446655440000"
	)
	handler := newNodePoolValidationTestHandler(t)
	body := `{"metadata":{"name":"pool-a","namespace":"cluster-` + clusterID + `"},"spec":{"nodePool":{"replicas":-1}}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v0/nodepools", strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), middleware.ContextKeyAccountID, accountID))
	response := httptest.NewRecorder()

	handler.Create(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Create() status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "spec.nodePool.replicas") {
		t.Fatalf("Create() response does not identify replicas: %s", response.Body.String())
	}
}

func TestNodePoolHandlerUpdateRejectsNegativeReplicas(t *testing.T) {
	const (
		accountID = "123456789012"
		clusterID = "550e8400-e29b-41d4-a716-446655440000"
		poolName  = "pool-a"
	)
	object := &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      poolName,
			Namespace: hyperfleetdb.ClusterNSPrefix + clusterID,
			Labels:    map[string]string{"hyperfleet.io/account-id": accountID},
		},
	}
	handler := newNodePoolValidationTestHandler(t, object)
	body := `{"spec":{"nodePool":{"replicas":-1}}}`
	request := httptest.NewRequest(http.MethodPut, "/api/v0/nodepools/"+poolName+"?clusterId="+clusterID, strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), middleware.ContextKeyAccountID, accountID))
	request = mux.SetURLVars(request, map[string]string{"id": poolName})
	response := httptest.NewRecorder()

	handler.Update(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Update() status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "spec.nodePool.replicas") {
		t.Fatalf("Update() response does not identify replicas: %s", response.Body.String())
	}
}

func newNodePoolValidationTestHandler(t *testing.T, objects ...client.Object) *NodePoolHandler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := hyperfleetv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register NodePool API types: %v", err)
	}
	dbClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	path := filepath.Join(t.TempDir(), "authz.json")
	if err := os.WriteFile(path, []byte(`{"formatVersion":1,"registeredAccounts":[],"policies":[],"attachments":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.LoadConfig(path, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	return NewNodePoolHandler(hyperfleetdb.NewClientFrom(dbClient, logger), authorizer, logger)
}

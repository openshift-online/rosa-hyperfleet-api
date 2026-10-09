package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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
	const accountID = "123456789012"
	handler := newNodePoolValidationTestHandler(t)
	body := `{"metadata":{"name":"test-cluster.pool-a","namespace":"account-` + accountID + `"},"spec":{"nodePool":{"replicas":-1}}}`
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
		poolName  = "pool-a"
	)
	object := &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      poolName,
			Namespace: "account-" + accountID,
			Labels:    map[string]string{"hyperfleet.io/account-id": accountID},
		},
	}
	handler := newNodePoolValidationTestHandler(t, object)
	body := `{"spec":{"nodePool":{"replicas":-1}}}`
	request := httptest.NewRequest(http.MethodPut, "/api/v0/nodepools/"+poolName, strings.NewReader(body))
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
	if !json.Valid(response.Body.Bytes()) {
		t.Fatalf("Update() response contains multiple JSON documents: %s", response.Body.String())
	}
}

func TestNodePoolHandlerUpdateReturnsMultipleValidationErrors(t *testing.T) {
	const (
		accountID = "123456789012"
		poolName  = "pool-a"
	)
	object := &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      poolName,
			Namespace: "account-" + accountID,
			Labels:    map[string]string{"hyperfleet.io/account-id": accountID},
		},
		Spec: hyperfleetv1alpha1.NodePoolSpec{
			NodePool: hyperfleetv1alpha1.NodePoolSpecPassthrough{
				Platform: hypershiftv1beta1.NodePoolPlatform{Type: hypershiftv1beta1.AWSPlatform},
			},
		},
	}
	handler := newNodePoolValidationTestHandler(t, object)
	body := `{"spec":{"nodePool":{"platform":{"type":"Azure"},"replicas":-1}}}`
	request := httptest.NewRequest(http.MethodPut, "/api/v0/nodepools/"+poolName, strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), middleware.ContextKeyAccountID, accountID))
	request = mux.SetURLVars(request, map[string]string{"id": poolName})
	response := httptest.NewRecorder()

	handler.Update(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Update() status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}
	var status metav1.Status
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatalf("Update() response is not valid Status JSON: %v; body=%s", err, response.Body.String())
	}
	if status.Details == nil {
		t.Fatalf("Update() response has no validation causes: %s", response.Body.String())
	}
	gotCauses := make(map[string]metav1.StatusCause, len(status.Details.Causes))
	for _, cause := range status.Details.Causes {
		gotCauses[cause.Field] = cause
	}
	wantCauses := map[string]string{
		"spec.nodePool.platform.type": "field is immutable and cannot be changed after creation",
		"spec.nodePool.replicas":      "must be greater than or equal to 0",
	}
	for field, wantMessage := range wantCauses {
		cause, ok := gotCauses[field]
		if !ok {
			t.Errorf("Update() validation causes %v do not include %q", status.Details.Causes, field)
			continue
		}
		if cause.Type != metav1.CauseTypeFieldValueInvalid {
			t.Errorf("cause %q type = %q, want %q", field, cause.Type, metav1.CauseTypeFieldValueInvalid)
		}
		if cause.Message != wantMessage {
			t.Errorf("cause %q message = %q, want %q", field, cause.Message, wantMessage)
		}
	}
	if len(status.Details.Causes) != 2 {
		t.Errorf("Update() returned %d validation causes, want 2: %+v", len(status.Details.Causes), status.Details.Causes)
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
	return NewNodePoolHandler(hyperfleetdb.NewClientFrom(dbClient, logger), logger)
}

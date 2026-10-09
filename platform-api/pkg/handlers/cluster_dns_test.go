package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

func clusterDNSRequestContext() context.Context {
	ctx := context.WithValue(context.Background(), middleware.ContextKeyAccountID, dnsTestAccountID)
	return context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::"+dnsTestAccountID+":user/test")
}

func clusterDNSRequestBody(baseDomain string) []byte {
	body, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"name": "dns-test-cluster"},
		"spec": map[string]any{
			"hostedCluster": map[string]any{
				"release":    map[string]any{"image": ""},
				"networking": map[string]any{},
				"platform":   map[string]any{"type": "AWS"},
				"dns":        map[string]any{"baseDomain": baseDomain},
			},
		},
	})
	return body
}

func newClusterDNSHandler(t *testing.T, objects []runtime.Object, funcs interceptor.Funcs) (*ClusterHandler, client.Client) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, fc := newDNSDomainFakeClient(t, objects, funcs)
	handler := NewClusterHandler(db, "https://oidc.example.com", 0, logger, "example.com")
	handler.generateID = func() string { return "cluster-id" }
	return handler, fc
}

func TestClusterHandlerCreateClaimsManagedDNSReservation(t *testing.T) {
	reservation := &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "0-abc12345", Namespace: "account-" + dnsTestAccountID},
		Spec:       hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "custom.0.example.com", ClusterArch: "hcp", UserDefined: true},
	}
	handler, fc := newClusterDNSHandler(t, []runtime.Object{reservation}, interceptor.Funcs{})
	request := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", strings.NewReader(string(clusterDNSRequestBody(reservation.Spec.BaseDomain)))).WithContext(clusterDNSRequestContext())
	response := httptest.NewRecorder()
	handler.Create(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}

	var storedReservation hyperfleetv1alpha1.DNSReservation
	if err := fc.Get(context.Background(), types.NamespacedName{Namespace: reservation.Namespace, Name: reservation.Name}, &storedReservation); err != nil {
		t.Fatalf("get DNS reservation: %v", err)
	}
	if owner := storedReservation.Labels[hyperfleetv1alpha1.DNSReservationClusterNamespaceLabel]; owner != hyperfleetdb.ClusterNSPrefix+"cluster-id" {
		t.Fatalf("DNS reservation owner = %q, want %q", owner, hyperfleetdb.ClusterNSPrefix+"cluster-id")
	}
	var storedCluster hyperfleetv1alpha1.Cluster
	if err := fc.Get(context.Background(), types.NamespacedName{Namespace: hyperfleetdb.ClusterNSPrefix + "cluster-id", Name: "dns-test-cluster"}, &storedCluster); err != nil {
		t.Fatalf("get created cluster: %v", err)
	}
}

func TestClusterHandlerCreateRejectsUnreservedManagedDNSDomain(t *testing.T) {
	handler, _ := newClusterDNSHandler(t, nil, interceptor.Funcs{})
	request := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", strings.NewReader(string(clusterDNSRequestBody("missing.0.example.com")))).WithContext(clusterDNSRequestContext())
	response := httptest.NewRecorder()
	handler.Create(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("Create status = %d, want %d: %s", response.Code, http.StatusNotFound, response.Body.String())
	}
}

func TestClusterHandlerCreateLeavesExternalAndGeneratedDNSUnclaimed(t *testing.T) {
	tests := []struct {
		name       string
		baseDomain string
	}{
		{name: "external domain", baseDomain: "customer.example.net"},
		{name: "generated domain", baseDomain: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _ := newClusterDNSHandler(t, nil, interceptor.Funcs{})
			request := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", strings.NewReader(string(clusterDNSRequestBody(tt.baseDomain)))).WithContext(clusterDNSRequestContext())
			response := httptest.NewRecorder()
			handler.Create(response, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("Create status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
			}
		})
	}
}

func TestClusterHandlerCreateFailureReleasesDNSClaim(t *testing.T) {
	reservation := &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "0-abc12345", Namespace: "account-" + dnsTestAccountID},
		Spec:       hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "custom.0.example.com", ClusterArch: "hcp", UserDefined: true},
	}
	funcs := interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
		if _, ok := obj.(*hyperfleetv1alpha1.Cluster); ok {
			return errors.New("cluster create failed")
		}
		return c.Create(ctx, obj, opts...)
	}}
	handler, fc := newClusterDNSHandler(t, []runtime.Object{reservation}, funcs)
	request := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", strings.NewReader(string(clusterDNSRequestBody(reservation.Spec.BaseDomain)))).WithContext(clusterDNSRequestContext())
	response := httptest.NewRecorder()
	handler.Create(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("Create status = %d, want %d: %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	var storedReservation hyperfleetv1alpha1.DNSReservation
	if err := fc.Get(context.Background(), types.NamespacedName{Namespace: reservation.Namespace, Name: reservation.Name}, &storedReservation); err != nil {
		t.Fatalf("get DNS reservation: %v", err)
	}
	if owner := storedReservation.Labels[hyperfleetv1alpha1.DNSReservationClusterNamespaceLabel]; owner != "" {
		t.Fatalf("DNS reservation claim was not released: owner %q", owner)
	}
}

func TestClusterHandlerClaimManagedDNSDomainErrors(t *testing.T) {
	tests := []struct {
		name        string
		reservation *hyperfleetv1alpha1.DNSReservation
		funcs       interceptor.Funcs
		wantCode    string
	}{
		{name: "reservation belongs to another cluster", reservation: &hyperfleetv1alpha1.DNSReservation{
			ObjectMeta: metav1.ObjectMeta{Name: "0-abc12345", Namespace: "account-" + dnsTestAccountID, Labels: map[string]string{hyperfleetv1alpha1.DNSReservationClusterNamespaceLabel: "cluster-other"}},
			Spec:       hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "custom.0.example.com", ClusterArch: "hcp", UserDefined: true},
		}, wantCode: "CLUSTERS-MGMT-CREATE-016"},
		{name: "reservation deletion is in progress", reservation: &hyperfleetv1alpha1.DNSReservation{
			ObjectMeta: metav1.ObjectMeta{Name: "0-abc12345", Namespace: "account-" + dnsTestAccountID, Labels: map[string]string{hyperfleetv1alpha1.DNSReservationDeletingLabel: "true"}},
			Spec:       hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "custom.0.example.com", ClusterArch: "hcp", UserDefined: true},
		}, wantCode: "CLUSTERS-MGMT-CREATE-016"},
		{name: "reservation lookup fails", funcs: interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*hyperfleetv1alpha1.DNSReservationList); ok {
				return errors.New("database unavailable")
			}
			return c.List(ctx, list, opts...)
		}}, wantCode: "CLUSTERS-MGMT-CREATE-014"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{}
			if tt.reservation != nil {
				objects = append(objects, tt.reservation)
			}
			handler, _ := newClusterDNSHandler(t, objects, tt.funcs)
			_, apiErr := handler.claimManagedDNSDomain(context.Background(), dnsTestAccountID, "custom.0.example.com", "cluster-1")
			if apiErr == nil || apiErr.Code != tt.wantCode {
				t.Fatalf("claim error = %#v, want code %s", apiErr, tt.wantCode)
			}
		})
	}
}

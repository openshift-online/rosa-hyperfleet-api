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

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
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
	if funcs.Create == nil {
		funcs.Create = func(ctx context.Context, underlying client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if cluster, ok := obj.(*hyperfleetv1alpha1.Cluster); ok && cluster.UID == "" {
				cluster.UID = types.UID("cluster-uid")
			}
			return underlying.Create(ctx, obj, opts...)
		}
	}
	db, fc := newDNSDomainFakeClient(t, objects, funcs)
	handler := NewClusterHandler(db, "https://oidc.example.com", 0, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com")
	return handler, fc
}

func TestClusterHandlerCreateClaimsManagedDNSReservation(t *testing.T) {
	reservation := dnsDomainReservation()
	reservation.Spec.BaseDomain = "custom.0.example.com"
	reservation.Status.BaseDomain = reservation.Spec.BaseDomain
	handler, fc := newClusterDNSHandler(t, []runtime.Object{reservation}, interceptor.Funcs{})

	request := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", strings.NewReader(string(clusterDNSRequestBody(reservation.Spec.BaseDomain)))).WithContext(clusterDNSRequestContext())
	response := httptest.NewRecorder()
	handler.Create(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}

	var storedReservation hyperfleetv1alpha1.DNSReservation
	if err := fc.Get(context.Background(), client.ObjectKeyFromObject(reservation), &storedReservation); err != nil {
		t.Fatalf("get DNS reservation: %v", err)
	}
	if owner := storedReservation.Labels[claimedByClusterUIDLabel]; owner != "cluster-uid" {
		t.Fatalf("DNS reservation owner = %q, want cluster-uid", owner)
	}

	var storedCluster hyperfleetv1alpha1.Cluster
	clusterKey := client.ObjectKey{Namespace: "account-" + dnsTestAccountID, Name: "dns-test-cluster"}
	if err := fc.Get(context.Background(), clusterKey, &storedCluster); err != nil {
		t.Fatalf("get created cluster: %v", err)
	}
	if storedCluster.Spec.DNSReservationID != string(reservation.UID) {
		t.Fatalf("cluster DNSReservationID = %q, want %q", storedCluster.Spec.DNSReservationID, reservation.UID)
	}
	if storedCluster.Spec.HostedCluster.DNS.BaseDomain != reservation.Spec.BaseDomain {
		t.Fatalf("cluster base domain = %q, want %q", storedCluster.Spec.HostedCluster.DNS.BaseDomain, reservation.Spec.BaseDomain)
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

func TestClusterHandlerCreateFailureLeavesDNSReservationUnclaimed(t *testing.T) {
	reservation := dnsDomainReservation()
	reservation.Spec.BaseDomain = "custom.0.example.com"
	reservation.Status.BaseDomain = reservation.Spec.BaseDomain
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
	if err := fc.Get(context.Background(), client.ObjectKeyFromObject(reservation), &storedReservation); err != nil {
		t.Fatalf("get DNS reservation: %v", err)
	}
	if owner := storedReservation.Labels[claimedByClusterUIDLabel]; owner != "" {
		t.Fatalf("DNS reservation was claimed before Cluster creation succeeded: owner %q", owner)
	}
}

func TestClusterHandlerResolveManagedDNSDomainErrors(t *testing.T) {
	claimed := dnsDomainReservation()
	claimed.Labels = map[string]string{claimedByClusterUIDLabel: "other-cluster"}
	notReady := dnsDomainReservation()
	notReady.Status.Phase = hyperfleetv1alpha1.DNSReservationPhasePending
	notReady.Status.BaseDomain = ""

	tests := []struct {
		name        string
		reservation *hyperfleetv1alpha1.DNSReservation
		funcs       interceptor.Funcs
		wantCode    string
	}{
		{
			name:        "reservation belongs to another cluster",
			reservation: claimed,
			wantCode:    "CLUSTERS-MGMT-CREATE-016",
		},
		{
			name:        "reservation is not ready",
			reservation: notReady,
			wantCode:    "CLUSTERS-MGMT-CREATE-016",
		},
		{
			name: "reservation lookup fails",
			funcs: interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*hyperfleetv1alpha1.DNSReservationList); ok {
					return errors.New("database unavailable")
				}
				return c.List(ctx, list, opts...)
			}},
			wantCode: "CLUSTERS-MGMT-CREATE-014",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{}
			if tt.reservation != nil {
				tt.reservation.Spec.BaseDomain = "custom.0.example.com"
				objects = append(objects, tt.reservation)
			}
			handler, _ := newClusterDNSHandler(t, objects, tt.funcs)
			_, apiErr := handler.resolveManagedDNSDomain(context.Background(), dnsTestAccountID, "custom.0.example.com")
			if apiErr == nil || apiErr.Code != tt.wantCode {
				t.Fatalf("resolve error = %#v, want code %s", apiErr, tt.wantCode)
			}
		})
	}
}

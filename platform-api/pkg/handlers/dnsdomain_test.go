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

	"github.com/gorilla/mux"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

const dnsTestAccountID = "123456789012"

func dnsDomainTestContext() context.Context {
	return context.WithValue(context.Background(), middleware.ContextKeyAccountID, dnsTestAccountID)
}

func newDNSDomainFakeClient(t *testing.T, objects []runtime.Object, funcs interceptor.Funcs) (*hyperfleetdb.Client, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := hyperfleetv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if funcs.Create == nil {
		funcs.Create = func(ctx context.Context, underlying client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if reservation, ok := obj.(*hyperfleetv1alpha1.DNSReservation); ok && reservation.UID == "" {
				reservation.UID = types.UID("550e8400-e29b-41d4-a716-446655440000")
			}
			return underlying.Create(ctx, obj, opts...)
		}
	}
	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&hyperfleetv1alpha1.DNSReservation{}, "metadata.uid", func(obj client.Object) []string {
			return []string{string(obj.GetUID())}
		}).
		WithRuntimeObjects(objects...).
		WithStatusSubresource(&hyperfleetv1alpha1.DNSReservation{}).
		WithInterceptorFuncs(funcs).
		Build()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return hyperfleetdb.NewClientFrom(fc, logger), fc
}

func dnsDomainReservation() *hyperfleetv1alpha1.DNSReservation {
	reservation := &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "0-abc12345",
			Namespace: "account-" + dnsTestAccountID,
			UID:       types.UID("550e8400-e29b-41d4-a716-446655440000"),
		},
		Spec: hyperfleetv1alpha1.DNSReservationSpec{
			BaseDomain:            "abc12345.0.example.com",
			ClusterArch:           dnsDomainArchHCP,
			UserDefined:           true,
			ClusterDeletionPolicy: hyperfleetv1alpha1.DNSReservationClusterDeletionPolicyRetain,
		},
	}
	reservation.Status.Phase = hyperfleetv1alpha1.DNSReservationPhaseReady
	reservation.Status.BaseDomain = reservation.Spec.BaseDomain
	reservation.Status.InternalHostedClusterName = hyperfleetv1alpha1.HostedClusterNameFromDNSReservation(reservation)
	return reservation
}

func TestDNSDomainHandlerCreate(t *testing.T) {
	db, fc := newDNSDomainFakeClient(t, nil, interceptor.Funcs{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewDNSDomainHandler(db, "example.com", logger)

	request := httptest.NewRequest(http.MethodPost, "/api/v0/dns_domains", strings.NewReader(`{}`)).WithContext(dnsDomainTestContext())
	response := httptest.NewRecorder()
	handler.Create(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}

	var result dnsDomainResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Kind != "DNSDomain" || result.ClusterArch != dnsDomainArchHCP || !result.UserDefined {
		t.Fatalf("unexpected response: %+v", result)
	}
	prefix := strings.TrimSuffix(strings.TrimSuffix(result.ID, ".example.com"), ".0")
	if len(prefix) != 8 {
		t.Fatalf("generated prefix = %q, want 8 hex characters", prefix)
	}

	var reservation hyperfleetv1alpha1.DNSReservation
	if err := fc.Get(context.Background(), client.ObjectKey{Namespace: "account-" + dnsTestAccountID, Name: "0-" + prefix}, &reservation); err != nil {
		t.Fatalf("get DNS reservation: %v", err)
	}
	if reservation.Spec.BaseDomain != result.ID || !reservation.Spec.UserDefined ||
		reservation.Spec.ClusterArch != dnsDomainArchHCP ||
		reservation.Status.Phase != hyperfleetv1alpha1.DNSReservationPhaseReady ||
		reservation.Status.BaseDomain != result.ID ||
		reservation.Status.InternalHostedClusterName == "" {
		t.Fatalf("stored reservation does not match response: %+v", reservation)
	}

	var index hyperfleetv1alpha1.Index
	if err := fc.Get(context.Background(), client.ObjectKey{Namespace: dnsDomainShardNamespace, Name: prefix}, &index); err != nil {
		t.Fatalf("get DNS index: %v", err)
	}
	if index.Labels["hyperfleet.io/owner-uid"] != string(reservation.UID) {
		t.Fatalf("DNS index owner labels = %#v", index.Labels)
	}
}

func TestDNSDomainHandlerCreateRejectsInvalidConfigurationAndRequest(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name   string
		suffix string
		body   string
		want   int
	}{
		{name: "invalid suffix", suffix: "bad_suffix", body: `{}`, want: http.StatusServiceUnavailable},
		{name: "suffix leaves no room for generated name", suffix: strings.Repeat("a", dnsMaxNameLength-dnsDomainGeneratedPrefixLength+1), body: `{}`, want: http.StatusServiceUnavailable},
		{name: "invalid JSON", suffix: "example.com", body: `{`, want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _ := newDNSDomainFakeClient(t, nil, interceptor.Funcs{})
			handler := NewDNSDomainHandler(db, tt.suffix, logger)
			request := httptest.NewRequest(http.MethodPost, "/api/v0/dns_domains", strings.NewReader(tt.body)).WithContext(dnsDomainTestContext())
			response := httptest.NewRecorder()
			handler.Create(response, request)
			if response.Code != tt.want {
				t.Fatalf("Create status = %d, want %d: %s", response.Code, tt.want, response.Body.String())
			}
		})
	}
}

func TestDNSDomainHandlerListFiltersReservations(t *testing.T) {
	objects := []runtime.Object{
		&hyperfleetv1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: "account-" + dnsTestAccountID}, Spec: hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "one.0.example.com", ClusterArch: "hcp", UserDefined: true}},
		&hyperfleetv1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "classic", Namespace: "account-" + dnsTestAccountID}, Spec: hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "two.0.example.com", ClusterArch: "classic", UserDefined: true}},
		&hyperfleetv1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "generated", Namespace: "account-" + dnsTestAccountID}, Spec: hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "three.0.example.com", ClusterArch: "hcp"}},
	}
	db, _ := newDNSDomainFakeClient(t, objects, interceptor.Funcs{})
	handler := NewDNSDomainHandler(db, "example.com", slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/api/v0/dns_domains", nil).WithContext(dnsDomainTestContext())
	response := httptest.NewRecorder()
	handler.List(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("List status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var result dnsDomainListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != "one.0.example.com" {
		t.Fatalf("unexpected DNS domain list: %+v", result)
	}
}

func TestDNSDomainHandlerDelete(t *testing.T) {
	tests := []struct {
		name       string
		claimedBy  string
		want       int
		wantExists bool
	}{
		{name: "deletes unclaimed reservation", want: http.StatusNoContent},
		{name: "rejects claimed reservation", claimedBy: "cluster-uid", want: http.StatusConflict, wantExists: true},
		{name: "missing reservation is not found", want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reservation := dnsDomainReservation()
			if tt.claimedBy != "" {
				reservation.Labels = map[string]string{claimedByClusterUIDLabel: tt.claimedBy}
			}
			objects := []runtime.Object{}
			if tt.name != "missing reservation is not found" {
				objects = append(objects, reservation)
			}
			db, fc := newDNSDomainFakeClient(t, objects, interceptor.Funcs{})
			handler := NewDNSDomainHandler(db, "example.com", slog.New(slog.NewTextHandler(io.Discard, nil)))
			request := httptest.NewRequest(http.MethodDelete, "/api/v0/dns_domains/abc12345.0.example.com", nil).WithContext(dnsDomainTestContext())
			request = mux.SetURLVars(request, map[string]string{"id": "abc12345.0.example.com"})
			response := httptest.NewRecorder()
			handler.Delete(response, request)
			if response.Code != tt.want {
				t.Fatalf("Delete status = %d, want %d: %s", response.Code, tt.want, response.Body.String())
			}

			err := fc.Get(context.Background(), client.ObjectKeyFromObject(reservation), &hyperfleetv1alpha1.DNSReservation{})
			if tt.wantExists && err != nil {
				t.Fatalf("reservation should still exist: %v", err)
			}
			if !tt.wantExists && tt.want == http.StatusNoContent && !apierrors.IsNotFound(err) {
				t.Fatalf("reservation should be deleted, got %v", err)
			}
		})
	}
}

func TestDNSDomainHandlerStorageFailures(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name  string
		funcs interceptor.Funcs
		want  int
	}{
		{name: "create index failure", funcs: interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*hyperfleetv1alpha1.Index); ok {
				return errors.New("index unavailable")
			}
			return c.Create(ctx, obj, opts...)
		}}, want: http.StatusInternalServerError},
		{name: "index owner lookup failure", funcs: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*hyperfleetv1alpha1.Index); ok {
				return errors.New("index lookup unavailable")
			}
			return c.Get(ctx, key, obj, opts...)
		}}, want: http.StatusInternalServerError},
		{name: "delete get failure", funcs: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*hyperfleetv1alpha1.DNSReservation); ok {
				return errors.New("reservation lookup unavailable")
			}
			return c.Get(ctx, key, obj, opts...)
		}}, want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _ := newDNSDomainFakeClient(t, []runtime.Object{dnsDomainReservation()}, tt.funcs)
			handler := NewDNSDomainHandler(db, "example.com", logger)
			var request *http.Request
			if tt.name == "delete get failure" {
				request = httptest.NewRequest(http.MethodDelete, "/api/v0/dns_domains/abc12345.0.example.com", nil).WithContext(dnsDomainTestContext())
				request = mux.SetURLVars(request, map[string]string{"id": "abc12345.0.example.com"})
			} else {
				request = httptest.NewRequest(http.MethodPost, "/api/v0/dns_domains", strings.NewReader(`{}`)).WithContext(dnsDomainTestContext())
			}
			response := httptest.NewRecorder()
			if tt.name == "delete get failure" {
				handler.Delete(response, request)
			} else {
				handler.Create(response, request)
			}
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, tt.want, response.Body.String())
			}
		})
	}
}

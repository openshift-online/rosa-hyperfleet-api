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
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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
	fc := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).WithInterceptorFuncs(funcs).Build()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return hyperfleetdb.NewClientFrom(fc, logger), fc
}

func TestDNSDomainHandlerCreate(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := hyperfleetv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := hyperfleetdb.NewClientFrom(fc, logger)
	handler := NewDNSDomainHandler(db, "example.com", logger)

	request := httptest.NewRequest(http.MethodPost, "/api/v0/dns_domains", strings.NewReader(`{"cluster_arch":"hcp"}`)).WithContext(dnsDomainTestContext())
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
	if err := fc.Get(context.Background(), types.NamespacedName{Namespace: "account-" + dnsTestAccountID, Name: "0-" + prefix}, &reservation); err != nil {
		t.Fatalf("get DNS reservation: %v", err)
	}
	if reservation.Spec.BaseDomain != result.ID || reservation.Spec.IndexRef.Name != prefix {
		t.Fatalf("stored reservation does not match response: %+v", reservation.Spec)
	}
	var index hyperfleetv1alpha1.Index
	if err := fc.Get(context.Background(), types.NamespacedName{Namespace: dnsDomainShardNamespace, Name: prefix}, &index); err != nil {
		t.Fatalf("get DNS index: %v", err)
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
		{name: "unsupported architecture", suffix: "example.com", body: `{"cluster_arch":"classic"}`, want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = hyperfleetv1alpha1.AddToScheme(scheme)
			fc := fake.NewClientBuilder().WithScheme(scheme).Build()
			handler := NewDNSDomainHandler(hyperfleetdb.NewClientFrom(fc, logger), tt.suffix, logger)
			request := httptest.NewRequest(http.MethodPost, "/api/v0/dns_domains", strings.NewReader(tt.body)).WithContext(dnsDomainTestContext())
			response := httptest.NewRecorder()
			handler.Create(response, request)
			if response.Code != tt.want {
				t.Fatalf("Create status = %d, want %d: %s", response.Code, tt.want, response.Body.String())
			}
		})
	}
}

func TestDNSDomainHandlerCreateStorageFailures(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	requestBody := `{"cluster_arch":"hcp"}`
	tests := []struct {
		name   string
		create func(client.Object) error
		delete func(client.Object) error
		want   int
	}{
		{
			name: "index storage error",
			create: func(obj client.Object) error {
				if _, ok := obj.(*hyperfleetv1alpha1.Index); ok {
					return errors.New("index unavailable")
				}
				return nil
			},
			want: http.StatusInternalServerError,
		},
		{
			name: "index name collision exhausts retries",
			create: func(obj client.Object) error {
				if _, ok := obj.(*hyperfleetv1alpha1.Index); ok {
					return apierrors.NewAlreadyExists(hyperfleetv1alpha1.GroupVersion.WithResource("indexes").GroupResource(), obj.GetName())
				}
				return nil
			},
			want: http.StatusConflict,
		},
		{
			name: "reservation storage error cleans up index",
			create: func(obj client.Object) error {
				if _, ok := obj.(*hyperfleetv1alpha1.DNSReservation); ok {
					return errors.New("reservation unavailable")
				}
				return nil
			},
			want: http.StatusInternalServerError,
		},
		{
			name: "reservation collision retries",
			create: func(obj client.Object) error {
				if _, ok := obj.(*hyperfleetv1alpha1.DNSReservation); ok {
					return apierrors.NewAlreadyExists(hyperfleetv1alpha1.GroupVersion.WithResource("dnsreservations").GroupResource(), obj.GetName())
				}
				return nil
			},
			want: http.StatusConflict,
		},
		{
			name: "reservation failure with index cleanup failure",
			create: func(obj client.Object) error {
				if _, ok := obj.(*hyperfleetv1alpha1.DNSReservation); ok {
					return errors.New("reservation unavailable")
				}
				return nil
			},
			delete: func(obj client.Object) error {
				if _, ok := obj.(*hyperfleetv1alpha1.Index); ok {
					return errors.New("index cleanup failed")
				}
				return nil
			},
			want: http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			funcs := interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if err := tt.create(obj); err != nil {
						return err
					}
					return c.Create(ctx, obj, opts...)
				},
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if tt.delete != nil {
						if err := tt.delete(obj); err != nil {
							return err
						}
					}
					return c.Delete(ctx, obj, opts...)
				},
			}
			db, _ := newDNSDomainFakeClient(t, nil, funcs)
			handler := NewDNSDomainHandler(db, "example.com", logger)
			request := httptest.NewRequest(http.MethodPost, "/api/v0/dns_domains", strings.NewReader(requestBody)).WithContext(dnsDomainTestContext())
			response := httptest.NewRecorder()
			handler.Create(response, request)
			if response.Code != tt.want {
				t.Fatalf("Create status = %d, want %d: %s", response.Code, tt.want, response.Body.String())
			}
		})
	}
}

func TestDNSDomainHandlerListFiltersReservations(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = hyperfleetv1alpha1.AddToScheme(scheme)
	objects := []runtime.Object{
		&hyperfleetv1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: "account-" + dnsTestAccountID}, Spec: hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "one.0.example.com", ClusterArch: "hcp", UserDefined: true}},
		&hyperfleetv1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "classic", Namespace: "account-" + dnsTestAccountID}, Spec: hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "two.0.example.com", ClusterArch: "classic", UserDefined: true}},
		&hyperfleetv1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "generated", Namespace: "account-" + dnsTestAccountID}, Spec: hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "three.0.example.com", ClusterArch: "hcp"}},
	}
	fc := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewDNSDomainHandler(hyperfleetdb.NewClientFrom(fc, logger), "example.com", logger)
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

type dnsDomainFailingWriter struct {
	header http.Header
}

func (w *dnsDomainFailingWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*dnsDomainFailingWriter) Write([]byte) (int, error) {
	return 0, errors.New("response write failed")
}

func (*dnsDomainFailingWriter) WriteHeader(int) {}

func TestDNSDomainHandlerListStorageAndWriteFailures(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reservation := &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "0-abc12345", Namespace: "account-" + dnsTestAccountID},
		Spec:       hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "abc12345.0.example.com", ClusterArch: dnsDomainArchHCP, UserDefined: true},
	}
	t.Run("storage failure writes an API error", func(t *testing.T) {
		funcs := interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*hyperfleetv1alpha1.DNSReservationList); ok {
				return errors.New("database unavailable")
			}
			return c.List(ctx, list, opts...)
		}}
		db, _ := newDNSDomainFakeClient(t, nil, funcs)
		handler := NewDNSDomainHandler(db, "example.com", logger)
		request := httptest.NewRequest(http.MethodGet, "/api/v0/dns_domains", nil).WithContext(dnsDomainTestContext())
		handler.List(&dnsDomainFailingWriter{}, request)
	})
	t.Run("response write failure is logged", func(t *testing.T) {
		db, _ := newDNSDomainFakeClient(t, []runtime.Object{reservation}, interceptor.Funcs{})
		handler := NewDNSDomainHandler(db, "example.com", logger)
		request := httptest.NewRequest(http.MethodGet, "/api/v0/dns_domains", nil).WithContext(dnsDomainTestContext())
		handler.List(&dnsDomainFailingWriter{}, request)
	})
}

func TestDNSDomainHandlerDelete(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name          string
		labels        map[string]string
		clusterDomain string
		want          int
	}{
		{name: "deletes reservation and index", want: http.StatusNoContent},
		{name: "rejects claimed reservation", labels: map[string]string{hyperfleetv1alpha1.DNSReservationClusterNamespaceLabel: "cluster-1"}, want: http.StatusConflict},
		{name: "rejects reservation used by cluster", clusterDomain: "custom.0.example.com", want: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = hyperfleetv1alpha1.AddToScheme(scheme)
			reservation := &hyperfleetv1alpha1.DNSReservation{
				ObjectMeta: metav1.ObjectMeta{Name: "0-abc12345", Namespace: "account-" + dnsTestAccountID, Labels: tt.labels},
				Spec:       hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "custom.0.example.com", ClusterArch: dnsDomainArchHCP, UserDefined: true, IndexRef: hyperfleetv1alpha1.IndexRef{Namespace: dnsDomainShardNamespace, Name: "abc12345"}},
			}
			index := &hyperfleetv1alpha1.Index{ObjectMeta: metav1.ObjectMeta{Name: "abc12345", Namespace: dnsDomainShardNamespace}}
			objects := []runtime.Object{reservation, index}
			if tt.clusterDomain != "" {
				objects = append(objects, &hyperfleetv1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster-1", Namespace: "cluster-1", Labels: map[string]string{"hyperfleet.io/account-id": dnsTestAccountID}}, Spec: hyperfleetv1alpha1.ClusterSpec{HostedCluster: hyperfleetv1alpha1.HostedClusterSpecPassthrough{DNS: hypershiftv1beta1.DNSSpec{BaseDomain: tt.clusterDomain}}}})
			}
			fc := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
			handler := NewDNSDomainHandler(hyperfleetdb.NewClientFrom(fc, logger), "example.com", logger)
			request := httptest.NewRequest(http.MethodDelete, "/api/v0/dns_domains/custom.0.example.com", nil).WithContext(dnsDomainTestContext())
			request = mux.SetURLVars(request, map[string]string{"id": "custom.0.example.com"})
			response := httptest.NewRecorder()
			handler.Delete(response, request)
			if response.Code != tt.want {
				t.Fatalf("Delete status = %d, want %d: %s", response.Code, tt.want, response.Body.String())
			}
			if tt.want == http.StatusNoContent {
				var gotReservation hyperfleetv1alpha1.DNSReservation
				if err := fc.Get(context.Background(), types.NamespacedName{Namespace: reservation.Namespace, Name: reservation.Name}, &gotReservation); !apierrors.IsNotFound(err) {
					t.Fatalf("reservation still exists or lookup failed: %v", err)
				}
				var gotIndex hyperfleetv1alpha1.Index
				if err := fc.Get(context.Background(), types.NamespacedName{Namespace: index.Namespace, Name: index.Name}, &gotIndex); !apierrors.IsNotFound(err) {
					t.Fatalf("index still exists or lookup failed: %v", err)
				}
			}
		})
	}
}

func TestDNSDomainHandlerDeleteStorageFailures(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reservation := &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "0-abc12345", Namespace: "account-" + dnsTestAccountID},
		Spec:       hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "custom.0.example.com", ClusterArch: dnsDomainArchHCP, UserDefined: true, IndexRef: hyperfleetv1alpha1.IndexRef{Namespace: dnsDomainShardNamespace, Name: "abc12345"}},
	}
	index := &hyperfleetv1alpha1.Index{ObjectMeta: metav1.ObjectMeta{Name: "abc12345", Namespace: dnsDomainShardNamespace}}
	tests := []struct {
		name    string
		objects []runtime.Object
		funcs   interceptor.Funcs
		want    int
	}{
		{
			name: "reservation list error",
			funcs: interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*hyperfleetv1alpha1.DNSReservationList); ok {
					return errors.New("list unavailable")
				}
				return c.List(ctx, list, opts...)
			}},
			want: http.StatusInternalServerError,
		},
		{
			name: "reservation not found",
			want: http.StatusNotFound,
		},
		{
			name:    "cluster list error",
			objects: []runtime.Object{reservation.DeepCopy()},
			funcs: interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*hyperfleetv1alpha1.ClusterList); ok {
					return errors.New("cluster list unavailable")
				}
				return c.List(ctx, list, opts...)
			}},
			want: http.StatusInternalServerError,
		},
		{
			name:    "reservation get error while fencing deletion",
			objects: []runtime.Object{reservation.DeepCopy()},
			funcs: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*hyperfleetv1alpha1.DNSReservation); ok {
					return errors.New("reservation lookup unavailable")
				}
				return c.Get(ctx, key, obj, opts...)
			}},
			want: http.StatusInternalServerError,
		},
		{
			name:    "index deletion error leaves reservation for retry",
			objects: []runtime.Object{reservation.DeepCopy(), index.DeepCopy()},
			funcs: interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*hyperfleetv1alpha1.Index); ok {
					return errors.New("index delete unavailable")
				}
				return c.Delete(ctx, obj, opts...)
			}},
			want: http.StatusInternalServerError,
		},
		{
			name:    "reservation deletion error after index release",
			objects: []runtime.Object{reservation.DeepCopy(), index.DeepCopy()},
			funcs: interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*hyperfleetv1alpha1.DNSReservation); ok {
					return errors.New("reservation delete unavailable")
				}
				return c.Delete(ctx, obj, opts...)
			}},
			want: http.StatusInternalServerError,
		},
		{
			name:    "missing index is tolerated",
			objects: []runtime.Object{reservation.DeepCopy()},
			want:    http.StatusNoContent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _ := newDNSDomainFakeClient(t, tt.objects, tt.funcs)
			handler := NewDNSDomainHandler(db, "example.com", logger)
			request := httptest.NewRequest(http.MethodDelete, "/api/v0/dns_domains/custom.0.example.com", nil).WithContext(dnsDomainTestContext())
			request = mux.SetURLVars(request, map[string]string{"id": "custom.0.example.com"})
			response := httptest.NewRecorder()
			handler.Delete(response, request)
			if response.Code != tt.want {
				t.Fatalf("Delete status = %d, want %d: %s", response.Code, tt.want, response.Body.String())
			}
		})
	}
}

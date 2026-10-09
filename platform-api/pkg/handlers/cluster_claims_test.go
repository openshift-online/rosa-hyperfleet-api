package handlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
)

type fakeClusterClaimableResource struct {
	requestedUID      string
	uid               string
	storedClaimedBy   string
	workingClaimedBy  string
	storedFinalizers  []string
	workingFinalizers []string
	ready             bool
	loadErr           error
	saveErr           error
	saveCalls         int
	onFirstSave       func(*fakeClusterClaimableResource) error
}

func (r *fakeClusterClaimableResource) LoadByUID(context.Context) error {
	r.workingClaimedBy = r.storedClaimedBy
	r.workingFinalizers = append([]string(nil), r.storedFinalizers...)
	return r.loadErr
}

func (r *fakeClusterClaimableResource) RequestedUID() string { return r.requestedUID }
func (r *fakeClusterClaimableResource) UID() string          { return r.uid }
func (r *fakeClusterClaimableResource) ClaimedByClusterUID() string {
	return r.workingClaimedBy
}
func (r *fakeClusterClaimableResource) SetClaimedByClusterUID(uid string) {
	r.workingClaimedBy = uid
}
func (r *fakeClusterClaimableResource) ReadyForClusterClaim() bool { return r.ready }
func (r *fakeClusterClaimableResource) Save(context.Context) error {
	r.saveCalls++
	if r.saveCalls == 1 && r.onFirstSave != nil {
		return r.onFirstSave(r)
	}
	if r.saveErr != nil {
		return r.saveErr
	}
	r.storedClaimedBy = r.workingClaimedBy
	r.storedFinalizers = append([]string(nil), r.workingFinalizers...)
	return nil
}
func (r *fakeClusterClaimableResource) NotFoundAPIError() *APIError {
	return &APIError{Code: "CLAIM-NOT-FOUND"}
}
func (r *fakeClusterClaimableResource) NotReadyAPIError() *APIError {
	return &APIError{Code: "CLAIM-NOT-READY"}
}
func (r *fakeClusterClaimableResource) InUseAPIError() *APIError {
	return &APIError{Code: "CLAIM-IN-USE"}
}

func TestClaimResourceRetriesConflictAndPreservesConcurrentMetadata(t *testing.T) {
	resource := &fakeClusterClaimableResource{
		requestedUID: "resource-uid",
		uid:          "resource-uid",
		ready:        true,
		onFirstSave: func(r *fakeClusterClaimableResource) error {
			r.storedFinalizers = append(r.storedFinalizers, "controller-finalizer")
			return apierrors.NewConflict(schema.GroupResource{Group: "hyperfleet.io", Resource: "claimables"}, "resource", errors.New("concurrent finalizer update"))
		},
	}

	apiErr, err := claimResource(context.Background(), "cluster-uid", resource)
	if err != nil {
		t.Fatalf("claimResource returned error: %v", err)
	}
	if apiErr != nil {
		t.Fatalf("claimResource returned API error: %v", apiErr)
	}
	if resource.saveCalls != 2 {
		t.Fatalf("Save called %d times, want one conflict retry", resource.saveCalls)
	}
	if resource.storedClaimedBy != "cluster-uid" {
		t.Errorf("claimed-by-cluster-uid = %q, want cluster-uid", resource.storedClaimedBy)
	}
	if len(resource.storedFinalizers) != 1 || resource.storedFinalizers[0] != "controller-finalizer" {
		t.Error("concurrent finalizer update was lost during claim retry")
	}
}

func TestClaimResourceReturnsInUseOnlyAfterFreshRead(t *testing.T) {
	resource := &fakeClusterClaimableResource{
		requestedUID: "resource-uid",
		uid:          "resource-uid",
		ready:        true,
		onFirstSave: func(r *fakeClusterClaimableResource) error {
			r.storedClaimedBy = "other-cluster-uid"
			return apierrors.NewConflict(schema.GroupResource{Group: "hyperfleet.io", Resource: "claimables"}, "resource", errors.New("concurrent claim"))
		},
	}

	apiErr, err := claimResource(context.Background(), "cluster-uid", resource)
	if err != nil {
		t.Fatalf("claimResource returned error: %v", err)
	}
	if apiErr == nil || apiErr.Code != "CLAIM-IN-USE" {
		t.Fatalf("claimResource API error = %v, want CLAIM-IN-USE", apiErr)
	}
	if resource.saveCalls != 1 {
		t.Fatalf("Save called %d times, want no update after observing the competing claim", resource.saveCalls)
	}
	if resource.storedClaimedBy != "other-cluster-uid" {
		t.Errorf("competing claim = %q, want other-cluster-uid", resource.storedClaimedBy)
	}
}

func TestClaimResourceStateHandling(t *testing.T) {
	loadFailure := errors.New("resource store unavailable")
	saveFailure := errors.New("resource update failed")
	tests := []struct {
		name                string
		resource            *fakeClusterClaimableResource
		clusterUID          string
		wantAPIError        string
		wantErr             error
		wantSaveCalls       int
		wantStoredClaimedBy string
	}{
		{
			name: "not found during load maps to API error",
			resource: &fakeClusterClaimableResource{
				requestedUID: "resource-uid",
				loadErr:      apierrors.NewNotFound(schema.GroupResource{Resource: "claimables"}, "resource-uid"),
			},
			clusterUID:   "cluster-uid",
			wantAPIError: "CLAIM-NOT-FOUND",
		},
		{
			name: "other load error is returned",
			resource: &fakeClusterClaimableResource{
				requestedUID: "resource-uid",
				loadErr:      loadFailure,
			},
			clusterUID: "cluster-uid",
			wantErr:    loadFailure,
		},
		{
			name: "UID mismatch maps to not found",
			resource: &fakeClusterClaimableResource{
				requestedUID: "requested-uid",
				uid:          "different-uid",
			},
			clusterUID:   "cluster-uid",
			wantAPIError: "CLAIM-NOT-FOUND",
		},
		{
			name: "same cluster claim is idempotent",
			resource: &fakeClusterClaimableResource{
				requestedUID:    "resource-uid",
				uid:             "resource-uid",
				storedClaimedBy: "cluster-uid",
				ready:           true,
			},
			clusterUID:          "cluster-uid",
			wantStoredClaimedBy: "cluster-uid",
		},
		{
			name: "claim by another cluster maps to in use",
			resource: &fakeClusterClaimableResource{
				requestedUID:    "resource-uid",
				uid:             "resource-uid",
				storedClaimedBy: "other-cluster-uid",
				ready:           true,
			},
			clusterUID:          "cluster-uid",
			wantAPIError:        "CLAIM-IN-USE",
			wantStoredClaimedBy: "other-cluster-uid",
		},
		{
			name: "unready resource maps to not ready",
			resource: &fakeClusterClaimableResource{
				requestedUID: "resource-uid",
				uid:          "resource-uid",
			},
			clusterUID:   "cluster-uid",
			wantAPIError: "CLAIM-NOT-READY",
		},
		{
			name: "save error is returned",
			resource: &fakeClusterClaimableResource{
				requestedUID: "resource-uid",
				uid:          "resource-uid",
				ready:        true,
				saveErr:      saveFailure,
			},
			clusterUID:    "cluster-uid",
			wantErr:       saveFailure,
			wantSaveCalls: 1,
		},
		{
			name: "ready unclaimed resource is claimed",
			resource: &fakeClusterClaimableResource{
				requestedUID: "resource-uid",
				uid:          "resource-uid",
				ready:        true,
			},
			clusterUID:          "cluster-uid",
			wantSaveCalls:       1,
			wantStoredClaimedBy: "cluster-uid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiErr, err := claimResource(context.Background(), tt.clusterUID, tt.resource)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("claimResource error = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("claimResource returned unexpected error: %v", err)
			}
			if tt.wantAPIError == "" {
				if apiErr != nil {
					t.Fatalf("claimResource API error = %v, want nil", apiErr)
				}
			} else if apiErr == nil || apiErr.Code != tt.wantAPIError {
				t.Fatalf("claimResource API error = %v, want %s", apiErr, tt.wantAPIError)
			}
			if tt.resource.saveCalls != tt.wantSaveCalls {
				t.Errorf("Save called %d times, want %d", tt.resource.saveCalls, tt.wantSaveCalls)
			}
			if tt.resource.storedClaimedBy != tt.wantStoredClaimedBy {
				t.Errorf("stored claim = %q, want %q", tt.resource.storedClaimedBy, tt.wantStoredClaimedBy)
			}
		})
	}
}

func TestDNSReservationClaimableResource(t *testing.T) {
	reservation := &hyperfleetv1alpha1.DNSReservation{}
	resource := &dnsReservationClaimableResource{resource: reservation}
	if resource.ReadyForClusterClaim() {
		t.Fatal("ReadyForClusterClaim() = true before reservation is Ready")
	}
	reservation.Status.Phase = hyperfleetv1alpha1.DNSReservationPhaseReady
	if resource.ReadyForClusterClaim() {
		t.Fatal("ReadyForClusterClaim() = true without a base domain")
	}
	reservation.Status.BaseDomain = "dns.example.com"
	if !resource.ReadyForClusterClaim() {
		t.Fatal("ReadyForClusterClaim() = false for a Ready reservation with a base domain")
	}

	resource.SetClaimedByClusterUID("cluster-uid")
	if got := resource.ClaimedByClusterUID(); got != "cluster-uid" {
		t.Errorf("claimed-by-cluster-uid = %q, want cluster-uid", got)
	}
	if got := resource.NotFoundAPIError(); got.Code != ErrClusterCreateDNSReservationNotFound.Code {
		t.Errorf("NotFoundAPIError code = %q, want %q", got.Code, ErrClusterCreateDNSReservationNotFound.Code)
	}
	if got := resource.NotReadyAPIError(); got.Code != ErrClusterCreateDNSReservationNotReady.Code {
		t.Errorf("NotReadyAPIError code = %q, want %q", got.Code, ErrClusterCreateDNSReservationNotReady.Code)
	}
	if got := resource.InUseAPIError(); got.Code != ErrClusterCreateDNSReservationInUse.Code {
		t.Errorf("InUseAPIError code = %q, want %q", got.Code, ErrClusterCreateDNSReservationInUse.Code)
	}
}

func TestOidcConfigClaimableResource(t *testing.T) {
	config := &hyperfleetv1alpha1.OidcConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "test-oidc"},
		Spec:       hyperfleetv1alpha1.OidcConfigSpec{Type: hyperfleetv1alpha1.OidcConfigTypeUnmanaged},
	}
	resource := &oidcConfigClaimableResource{resource: config}
	if resource.ReadyForClusterClaim() {
		t.Fatal("ReadyForClusterClaim() = true for a Pending unmanaged config")
	}
	config.Status.Phase = hyperfleetv1alpha1.OidcConfigPhaseReady
	if !resource.ReadyForClusterClaim() {
		t.Fatal("ReadyForClusterClaim() = false for a Ready unmanaged config")
	}
	config.Spec.Type = hyperfleetv1alpha1.OidcConfigTypeManaged
	config.Status.Phase = hyperfleetv1alpha1.OidcConfigPhasePending
	if !resource.ReadyForClusterClaim() {
		t.Fatal("ReadyForClusterClaim() = false for a Pending managed config")
	}
	config.Status.Phase = hyperfleetv1alpha1.OidcConfigPhaseError
	if resource.ReadyForClusterClaim() {
		t.Fatal("ReadyForClusterClaim() = true for an Error config")
	}

	resource.SetClaimedByClusterUID("cluster-uid")
	if got := resource.ClaimedByClusterUID(); got != "cluster-uid" {
		t.Errorf("claimed-by-cluster-uid = %q, want cluster-uid", got)
	}
	if got := resource.NotFoundAPIError(); got.Code != ErrClusterCreateOidcConfigNotFound.Code {
		t.Errorf("NotFoundAPIError code = %q, want %q", got.Code, ErrClusterCreateOidcConfigNotFound.Code)
	}
	if got := resource.NotReadyAPIError(); got.Code != ErrClusterCreateOidcConfigNotReady.Code {
		t.Errorf("NotReadyAPIError code = %q, want %q", got.Code, ErrClusterCreateOidcConfigNotReady.Code)
	}
	if got := resource.InUseAPIError(); got.Code != ErrClusterCreateOidcConfigInUse.Code {
		t.Errorf("InUseAPIError code = %q, want %q", got.Code, ErrClusterCreateOidcConfigInUse.Code)
	} else if reason, ok := got.Errors.(error); !ok || !strings.Contains(reason.Error(), config.Name) {
		t.Errorf("InUseAPIError errors = %v, want reason containing %q", got.Errors, config.Name)
	}
}

func TestResolveDNSReservation(t *testing.T) {
	claimedReservation := testDNSReservationCR(testAccountID)
	claimedReservation.Labels[claimedByClusterUIDLabel] = "existing-cluster-uid"
	missingBaseDomainReservation := testDNSReservationCR(testAccountID)
	missingBaseDomainReservation.Status.BaseDomain = ""
	notReadyReservation := testDNSReservationCR(testAccountID)
	notReadyReservation.Status.Phase = hyperfleetv1alpha1.DNSReservationPhasePending
	mismatchedUIDReservation := testDNSReservationCR(testAccountID)
	mismatchedUIDReservation.UID = "different-uid"

	tests := []struct {
		name             string
		requestedUID     string
		reservation      *hyperfleetv1alpha1.DNSReservation
		indexedUID       string
		registerAPITypes bool
		wantError        string
		wantUID          string
	}{
		{
			name:         "reservation UID is required",
			requestedUID: "",
			wantError:    ErrClusterCreateDNSReservationRequired.Code,
		},
		{
			name:             "not found",
			requestedUID:     "missing-uid",
			registerAPITypes: true,
			wantError:        ErrClusterCreateDNSReservationNotFound.Code,
		},
		{
			name:         "lookup failure",
			requestedUID: testDNSReservationUID,
			wantError:    ErrClusterCreateFailed.Code,
		},
		{
			name:             "returned UID does not match request",
			requestedUID:     "requested-uid",
			reservation:      mismatchedUIDReservation,
			indexedUID:       "requested-uid",
			registerAPITypes: true,
			wantError:        ErrClusterCreateDNSReservationNotFound.Code,
		},
		{
			name:             "reservation is not ready",
			requestedUID:     testDNSReservationUID,
			reservation:      notReadyReservation,
			registerAPITypes: true,
			wantError:        ErrClusterCreateDNSReservationNotReady.Code,
		},
		{
			name:             "ready reservation has no base domain",
			requestedUID:     testDNSReservationUID,
			reservation:      missingBaseDomainReservation,
			registerAPITypes: true,
			wantError:        ErrClusterCreateDNSReservationNotReady.Code,
		},
		{
			name:             "reservation is already claimed",
			requestedUID:     testDNSReservationUID,
			reservation:      claimedReservation,
			registerAPITypes: true,
			wantError:        ErrClusterCreateDNSReservationInUse.Code,
		},
		{
			name:             "ready unclaimed reservation",
			requestedUID:     testDNSReservationUID,
			reservation:      testDNSReservationCR(testAccountID),
			registerAPITypes: true,
			wantUID:          testDNSReservationUID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newDNSReservationResolverHandler(t, tt.reservation, tt.indexedUID, tt.registerAPITypes)
			reservation, apiErr := handler.resolveDNSReservation(context.Background(), testAccountID, tt.requestedUID)
			if tt.wantError != "" {
				if apiErr == nil || apiErr.Code != tt.wantError {
					t.Fatalf("resolveDNSReservation error = %v, want %s", apiErr, tt.wantError)
				}
				if reservation != nil {
					t.Errorf("resolveDNSReservation reservation = %v, want nil", reservation)
				}
				return
			}
			if apiErr != nil {
				t.Fatalf("resolveDNSReservation returned API error: %v", apiErr)
			}
			if reservation == nil || string(reservation.UID) != tt.wantUID {
				t.Fatalf("resolveDNSReservation UID = %v, want %q", reservation, tt.wantUID)
			}
		})
	}
}

func newDNSReservationResolverHandler(t *testing.T, reservation *hyperfleetv1alpha1.DNSReservation, indexedUID string, registerAPITypes bool) *ClusterHandler {
	t.Helper()

	scheme := runtime.NewScheme()
	if registerAPITypes {
		scheme = newTestScheme()
	}
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if registerAPITypes {
		builder = builder.WithIndex(&hyperfleetv1alpha1.DNSReservation{}, "metadata.uid", func(obj client.Object) []string {
			if indexedUID != "" {
				return []string{indexedUID}
			}
			return []string{string(obj.GetUID())}
		})
	}
	if reservation != nil {
		builder = builder.WithObjects(reservation)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := hyperfleetdb.NewClientFrom(builder.Build(), logger)
	return NewClusterHandler(db, "", 0, logger)
}

func TestClientIgnoreNotFound(t *testing.T) {
	missing := apierrors.NewNotFound(schema.GroupResource{Resource: "dnsreservations"}, "missing")
	if err := clientIgnoreNotFound(missing); err != nil {
		t.Errorf("clientIgnoreNotFound(NotFound) = %v, want nil", err)
	}

	other := errors.New("database unavailable")
	if err := clientIgnoreNotFound(other); err != other {
		t.Errorf("clientIgnoreNotFound(other) = %v, want original error", err)
	}
}

//go:build integration

package handlers

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	testutil "github.com/openshift-online/rosa-hyperfleet-api/platform-api/test/util"
)

func newTestScheme(t testing.TB) *runtime.Scheme {
	return testutil.NewScheme(t)
}

func testContext(accountID string) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::"+accountID+":user/test")
	return ctx
}

// metadataContinue extracts the metadata.continue token from a decoded JSON
// list response. Returns "" when no next page exists.
func metadataContinue(result map[string]any) string {
	meta, _ := result["metadata"].(map[string]any)
	token, _ := meta["continue"].(string)
	return token
}

// newIndexedFakeBuilder returns a fake client builder with field indexes
// matching the MatchingFields selectors used in production list queries.
// The real pgclient translates these to SQL; the fake client needs explicit indexers.
func newIndexedFakeBuilder(t testing.TB) *fake.ClientBuilder {
	t.Helper()
	scheme := newTestScheme(t)
	accountFieldKey := "metadata.labels." + hyperfleetdb.AccountIDLabel
	accountIndexer := func(o client.Object) []string {
		if v, ok := o.GetLabels()[hyperfleetdb.AccountIDLabel]; ok {
			return []string{v}
		}
		return nil
	}
	nameIndexer := func(o client.Object) []string { return []string{o.GetName()} }

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&hyperfleetv1alpha1.Cluster{}, accountFieldKey, accountIndexer).
		WithIndex(&hyperfleetv1alpha1.Cluster{}, "metadata.name", nameIndexer).
		WithIndex(&hyperfleetv1alpha1.NodePool{}, accountFieldKey, accountIndexer).
		WithIndex(&hyperfleetv1alpha1.NodePool{}, "metadata.name", nameIndexer)
}

package hyperfleetdb

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/pagination"
	testutil "github.com/openshift-online/rosa-hyperfleet-api/platform-api/test/util"
)

func testScheme(t testing.TB) *runtime.Scheme {
	return testutil.NewScheme(t)
}

// testFakeBuilder returns a fake client builder with field indexes matching
// what ListClusters/ListNodePools/FindClusterByName use via MatchingFields in production.
func testFakeBuilder(t testing.TB) *fake.ClientBuilder {
	t.Helper()
	accountFieldKey := "metadata.labels." + accountIDLabel
	accountIndexer := func(o client.Object) []string {
		if v, ok := o.GetLabels()[accountIDLabel]; ok {
			return []string{v}
		}
		return nil
	}
	nameIndexer := func(o client.Object) []string { return []string{o.GetName()} }
	return fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithIndex(&hyperfleetv1alpha1.Cluster{}, accountFieldKey, accountIndexer).
		WithIndex(&hyperfleetv1alpha1.Cluster{}, "metadata.name", nameIndexer).
		WithIndex(&hyperfleetv1alpha1.NodePool{}, accountFieldKey, accountIndexer).
		WithIndex(&hyperfleetv1alpha1.NodePool{}, "metadata.name", nameIndexer)
}

// testListOpts builds a ListOptions for a given accountID with default page size.
func testListOpts(accountID string) ListOptions {
	return ListOptions{
		AccountID: accountID,
		Options:   pagination.Options{Limit: 50},
	}
}

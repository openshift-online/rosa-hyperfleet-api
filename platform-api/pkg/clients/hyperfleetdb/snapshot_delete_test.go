package hyperfleetdb

import (
	"context"
	"io"
	"log/slog"
	"testing"

	v1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestSnapshotDeletesRejectInvalidIdentity(t *testing.T) {
	writes := 0
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithInterceptorFuncs(interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
		writes++
		return nil
	}}).Build()
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, namespace := range []string{"", "cluster-", "wrong", "id", "Cluster-id", "account-id", "\x00"} {
		t.Run(namespace, func(t *testing.T) {
			meta := metav1.ObjectMeta{Name: "name", Namespace: namespace, ResourceVersion: "1", Labels: map[string]string{accountIDLabel: "123456789012"}}
			clusterReason, poolReason := "invalid snapshot identity", "invalid snapshot identity"
			if namespace == "" || namespace == "cluster-" {
				clusterReason, poolReason = "invalid cluster namespace", "invalid node pool namespace"
			}
			if err := c.DeleteClusterObject(ctxAccount("123456789012"), &v1.Cluster{ObjectMeta: meta}); err == nil || err.Error() != clusterReason {
				t.Fatalf("cluster rejection = %v, want %s", err, clusterReason)
			}
			if err := c.DeleteNodePoolObject(ctxAccount("123456789012"), &v1.NodePool{ObjectMeta: meta}); err == nil || err.Error() != poolReason {
				t.Fatalf("pool rejection = %v, want %s", err, poolReason)
			}
		})
	}
	if writes != 0 {
		t.Errorf("invalid snapshot reached delete %d times", writes)
	}
}

func TestSnapshotDeletesDoNotReloadAndRequireVersion(t *testing.T) {
	const account = "123456789012"
	for _, kind := range []string{"Cluster", "NodePool", "OIDCConfig"} {
		for _, rv := range []string{"", "0", "-1", "invalid", "o0;1", "o-1;1", "1", "o5;123"} {
			t.Run(kind+"/"+rv, func(t *testing.T) {
				writes, reads := 0, 0
				fc := fake.NewClientBuilder().WithScheme(testScheme()).WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						reads++
						return nil
					},
					List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
						reads++
						return nil
					},
					Delete: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.DeleteOption) error {
						writes++
						if o.GetResourceVersion() != rv {
							t.Fatal("checked RV changed")
						}
						return nil
					},
				}).Build()
				c := NewClientFrom(fc, slog.New(slog.NewTextHandler(io.Discard, nil)))
				meta := metav1.ObjectMeta{Name: "name", Namespace: "cluster-id", ResourceVersion: rv, Labels: map[string]string{accountIDLabel: account}}
				var err error
				switch kind {
				case "Cluster":
					err = c.DeleteClusterObject(ctxAccount(account), &v1.Cluster{ObjectMeta: meta})
				case "NodePool":
					err = c.DeleteNodePoolObject(ctxAccount(account), &v1.NodePool{ObjectMeta: meta})
				case "OIDCConfig":
					meta.Namespace = "account-" + account
					err = c.DeleteOidcConfigObject(ctxAccount(account), &v1.OidcConfig{ObjectMeta: meta})
				}
				valid := rv == "1" || rv == "o5;123"
				if (err == nil) != valid || writes != map[bool]int{true: 1, false: 0}[valid] || reads != 0 {
					t.Fatalf("snapshot delete valid=%v err=%v writes=%d reads=%d", valid, err, writes, reads)
				}
			})
		}
	}
}

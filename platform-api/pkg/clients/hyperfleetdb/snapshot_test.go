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

func TestUpdatesRequireAuthorizedVersion(t *testing.T) {
	for _, rv := range []string{"", "0", "-1", "invalid"} {
		t.Run(rv, func(t *testing.T) {
			writes := 0
			fc := fake.NewClientBuilder().WithScheme(testScheme()).WithInterceptorFuncs(interceptor.Funcs{Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				writes++
				return nil
			}}).Build()
			c := NewClientFrom(fc, slog.New(slog.NewTextHandler(io.Discard, nil)))
			cr := &v1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "cluster-id", ResourceVersion: rv, Labels: map[string]string{accountIDLabel: "123456789012"}}}
			if err := c.UpdateCluster(ctxAccount("123456789012"), cr); err == nil || writes != 0 {
				t.Fatalf("unchecked cluster version wrote: err=%v writes=%d", err, writes)
			}
			np := &v1.NodePool{ObjectMeta: cr.ObjectMeta}
			if err := c.UpdateNodePool(ctxAccount("123456789012"), np); err == nil || writes != 0 {
				t.Fatalf("unchecked pool version wrote: err=%v writes=%d", err, writes)
			}
		})
	}
}

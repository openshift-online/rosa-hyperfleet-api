package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

// Clusters and node pools are routed by {name}; the authz middleware must
// treat those requests as single-resource actions on the named resource.
func TestAuthz_DerivesActionAndResourceFromRoute(t *testing.T) {
	a := NewAuthz(nil, true, "us-east-1", slog.Default())
	ctx := context.WithValue(context.Background(), ContextKeyAccountID, "123456789012")

	tests := []struct {
		method, path string
		vars         map[string]string
		wantAction   string
		wantResource string
	}{
		{http.MethodGet, "/api/v0/clusters", nil, "ListClusters", "*"},
		{http.MethodGet, "/api/v0/clusters/prod", map[string]string{"name": "prod"},
			"DescribeCluster", "arn:aws:rosa:us-east-1:123456789012:cluster/prod"},
		{http.MethodDelete, "/api/v0/clusters/prod", map[string]string{"name": "prod"},
			"DeleteCluster", "arn:aws:rosa:us-east-1:123456789012:cluster/prod"},
		{http.MethodGet, "/api/v0/nodepools/prod.workers", map[string]string{"name": "prod.workers"},
			"DescribeNodePool", "arn:aws:rosa:us-east-1:123456789012:nodepool/prod.workers"},
		{http.MethodGet, "/api/v0/nodepools", nil, "ListNodePools", "*"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, nil).WithContext(ctx)
			if tt.vars != nil {
				r = mux.SetURLVars(r, tt.vars)
			}
			if got := a.deriveAction(r); got != tt.wantAction {
				t.Errorf("action = %q, want %q", got, tt.wantAction)
			}
			if got := a.deriveResource(r); got != tt.wantResource {
				t.Errorf("resource = %q, want %q", got, tt.wantResource)
			}
		})
	}
}

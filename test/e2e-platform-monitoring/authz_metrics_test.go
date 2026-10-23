package e2e_monitoring_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	awstest "github.com/openshift-online/rosa-seaplane-api/test/helpers/aws"
)

func TestAuthzMetricValue(t *testing.T) {
	// Query failures and malformed samples must not become a successful zero baseline.
	for _, tc := range []struct {
		name, body string
		status     int
		want       float64
		hasError   bool
	}{
		{"zero baseline", `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"0"]}]}}`, http.StatusOK, 0, false},
		{"historical count", `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"7"]}]}}`, http.StatusOK, 7, false},
		{"query failure", `{"status":"error"}`, http.StatusOK, 0, true},
		{"unavailable", `{}`, http.StatusServiceUnavailable, 0, true},
		{"missing sample", `{"status":"success","data":{"resultType":"vector","result":[]}}`, http.StatusOK, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/query" || r.URL.Query().Get("query") != "sum(authz_requests_total) or vector(0)" {
					t.Errorf("unexpected metrics request: %s", r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			got, err := authzMetricValue(awstest.NewAPIClient(server.URL), "sum(authz_requests_total) or vector(0)")
			if (err != nil) != tc.hasError || (!tc.hasError && got != tc.want) {
				t.Fatalf("count = %v, error = %v; want count %v, hasError %v", got, err, tc.want, tc.hasError)
			}
		})
	}
}

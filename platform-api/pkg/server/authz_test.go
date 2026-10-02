package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/config"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const serverBundle = `formatVersion: 1
registeredAccounts: ["123456789012"]
policies:
  - id: read
    ownerAccountID: "123456789012"
    content: 'permit(principal, action in HyperFleet::Action::"ReadOnly", resource);'
  - id: unregistered-read
    ownerAccountID: "999999999999"
    content: 'permit(principal, action in HyperFleet::Action::"ReadOnly", resource);'
attachments:
  - id: user-read
    policyID: read
    principalARN: arn:aws:iam::123456789012:user/test
    bindingMode: exact-principal
    scope: global
  - id: unregistered-user-read
    policyID: unregistered-read
    principalARN: arn:aws:iam::999999999999:user/test
    bindingMode: exact-principal
    scope: global
`

func configuredResolver(t *testing.T, cfg *config.Config) *authz.ConfigResolver {
	t.Helper()
	cfg.Regional.AWSRegion = "us-east-1"
	cfg.Authz.Resolver = "config"
	cfg.Authz.ConfigFile = filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(cfg.Authz.ConfigFile, []byte(serverBundle), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := authz.LoadConfig(cfg.Authz.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func newConfiguredServer(t *testing.T, cfg *config.Config, db *hyperfleetdb.Client, logger *slog.Logger) (*Server, error) {
	t.Helper()
	resolver := configuredResolver(t, cfg)
	authorizer, err := authz.NewAuthorizer(resolver)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, db, authorizer, resolver.IsAccountRegistered, logger)
}

func TestNewRequiresDependencies(t *testing.T) {
	cfg := config.NewConfig()
	resolver := configuredResolver(t, cfg)
	authorizer, err := authz.NewAuthorizer(resolver)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name       string
		authorizer *authz.Authorizer
		lookup     func(context.Context, string) bool
	}{
		{"authorizer", nil, resolver.IsAccountRegistered},
		{"lookup", authorizer, nil},
		{"both", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, err := New(cfg, nil, tc.authorizer, tc.lookup, logger)
			if err == nil || server != nil {
				t.Fatalf("missing dependency accepted: server=%v error=%v", server, err)
			}
		})
	}
}

type resolutionProbe struct {
	resolver *authz.ConfigResolver
	calls    int
}

func (p *resolutionProbe) Resolve(ctx context.Context, identity authz.Identity) ([]authz.ResolvedBinding, error) {
	p.calls++
	return p.resolver.Resolve(ctx, identity)
}

func authzMetricSamples(t *testing.T) map[string]float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	samples := map[string]float64{}
	for _, family := range families {
		name := family.GetName()
		if !strings.HasPrefix(name, "authz_") {
			continue
		}
		for _, metric := range family.Metric {
			labels := ""
			for _, label := range metric.Label {
				labels += "/" + label.GetName() + "=" + label.GetValue()
			}
			if metric.Counter != nil {
				samples[name+labels] = metric.Counter.GetValue()
			}
			if metric.Histogram != nil {
				samples[name+labels] = float64(metric.Histogram.GetSampleCount())
			}
		}
	}
	return samples
}

func TestServerEnrollment(t *testing.T) {
	t.Setenv("TARGET_GROUP_ARN", "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/test/id")
	cfg := config.NewConfig()
	resolver := configuredResolver(t, cfg)
	probe := &resolutionProbe{resolver: resolver}
	authorizer, err := authz.NewAuthorizer(probe)
	if err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	if err := hyperfleetv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := hyperfleetdb.NewClientFrom(fake.NewClientBuilder().WithScheme(scheme).Build(), logger)
	srv, err := New(cfg, db, authorizer, resolver.IsAccountRegistered, logger)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, account, caller string
		wantStatus, wantResolutions int
	}{
		{"enrolled with grant", "/api/v0/clusters", "123456789012", "arn:aws:iam::123456789012:user/test", http.StatusOK, 1},
		{"enrolled without grant", "/api/v0/clusters", "123456789012", "arn:aws:iam::123456789012:user/no-grants", http.StatusForbidden, 1},
		{"unregistered with grant", "/api/v0/clusters", "999999999999", "arn:aws:iam::999999999999:user/test", http.StatusForbidden, 0},
		{"missing identity", "/api/v0/clusters", "", "", http.StatusForbidden, 0},
		{"mismatch", "/api/v0/clusters", "123456789012", "arn:aws:iam::999999999999:user/test", http.StatusForbidden, 0},
		{"malformed ARN", "/api/v0/clusters", "123456789012", "bad-arn", http.StatusForbidden, 0},
		{"live", "/api/v0/live", "", "", http.StatusOK, 0},
		{"ready", "/api/v0/ready", "", "", http.StatusOK, 0},
		{"info with malformed identity", "/api/v0/info", "123", "bad-arn", http.StatusOK, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := authzMetricSamples(t)
			calls := probe.calls
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set(middleware.HeaderAccountID, tc.account)
			req.Header.Set(middleware.HeaderCallerARN, tc.caller)
			w := httptest.NewRecorder()
			srv.apiServer.Handler.ServeHTTP(w, req)
			if w.Code != tc.wantStatus || probe.calls-calls != tc.wantResolutions {
				t.Fatalf("status=%d, resolutions=%d, body=%s", w.Code, probe.calls-calls, w.Body.String())
			}
			if tc.wantResolutions == 0 && !reflect.DeepEqual(before, authzMetricSamples(t)) {
				t.Fatal("public or admission rejection counted as authorization attempt")
			}
			if w.Code != http.StatusForbidden {
				return
			}
			var status metav1.Status
			if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if status.Kind != "Status" || status.Status != metav1.StatusFailure || status.Code != http.StatusForbidden || status.Reason != metav1.StatusReasonForbidden || strings.Contains(status.Message, tc.caller) && tc.caller != "" || status.Details != nil {
				t.Fatalf("unsafe or unstructured rejection: %+v", status)
			}
			if tc.name == "enrolled without grant" && strings.Contains(status.Message, "Account is not registered") {
				t.Fatal("missing grants confused with missing enrollment")
			}
		})
	}
}

func TestServerMultipleMetrics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	before := authzMetricSamples(t)
	for range 3 {
		srv, err := newConfiguredServer(t, config.NewConfig(), nil, logger)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		srv.metricsServer.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("metrics scrape failed: %d %s", w.Code, w.Body.String())
		}
		if !reflect.DeepEqual(before, authzMetricSamples(t)) {
			t.Fatal("constructing another server or scraping changed authorization samples")
		}
	}
}

func TestServerRateLimitBeforeAdmission(t *testing.T) {
	cfg := config.NewConfig()
	cfg.RateLimit = config.RateLimitConfig{Enabled: true, InMemory: true, DefaultRate: 1, DefaultBurst: 1, DefaultWindow: 60}
	resolver := configuredResolver(t, cfg)
	probe := &resolutionProbe{resolver: resolver}
	authorizer, err := authz.NewAuthorizer(probe)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	lookups := 0
	lookup := func(ctx context.Context, account string) bool {
		lookups++
		return resolver.IsAccountRegistered(ctx, account)
	}
	srv, err := New(cfg, nil, authorizer, lookup, logger)
	if err != nil {
		t.Fatal(err)
	}
	before := authzMetricSamples(t)
	for _, want := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters", nil)
		req.Header.Set(middleware.HeaderAccountID, "999999999999")
		req.Header.Set(middleware.HeaderCallerARN, "arn:aws:iam::999999999999:user/test")
		srv.apiServer.Handler.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("want %d, got %d: %s", want, w.Code, w.Body.String())
		}
	}
	if lookups != 1 || probe.calls != 0 || !reflect.DeepEqual(before, authzMetricSamples(t)) {
		t.Fatalf("rate-limited request reached admission or authorization: lookups=%d resolutions=%d", lookups, probe.calls)
	}
}

package authz

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func authzSampleTotals(t *testing.T, gatherer prometheus.Gatherer) map[string]float64 {
	t.Helper()
	families, err := gatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	totals := map[string]float64{}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "authz_") {
			continue
		}
		for _, metric := range family.Metric {
			totals[family.GetName()] += metric.GetCounter().GetValue() + float64(metric.GetHistogram().GetSampleCount())
		}
	}
	return totals
}

func TestDefaultMetrics(t *testing.T) {
	labels := map[string]string{"operation": string(DescribeCluster), "outcome": string(OutcomeDeny)}
	before := metricSample(t, prometheus.DefaultGatherer, "authz_requests_total", labels).GetCounter().GetValue()
	samples := metricSample(t, prometheus.DefaultGatherer, "authz_duration_seconds", labels).GetHistogram().GetSampleCount()
	attempt, err := DefaultMetrics.Start(DescribeCluster)
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.Finish(OutcomeDeny, StageNone); err != nil {
		t.Fatal(err)
	}
	if metricSample(t, prometheus.DefaultGatherer, "authz_requests_total", labels).GetCounter().GetValue()-before != 1 || metricSample(t, prometheus.DefaultGatherer, "authz_duration_seconds", labels).GetHistogram().GetSampleCount()-samples != 1 {
		t.Fatal("default registry did not receive one counter and histogram sample")
	}
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}
	span, err := metrics.Start(ListClusters)
	if err != nil {
		t.Fatal(err)
	}
	if err := span.Finish(OutcomeAllow, StageNone); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `authz_duration_seconds_bucket{operation="ListClusters",outcome="allow",le="+Inf"} 1`) {
		t.Fatalf("missing automatic +Inf sample: %s", recorder.Body.String())
	}
}

func TestLateAttemptFailure(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}
	a := fixtureAuthorizer(t, bundleFixture())
	attempt, err := metrics.Start(ListClusters)
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	if d, err := p.Check(t.Context(), ListClusters, testCollection()); err != nil || !d.Allowed {
		t.Fatalf("collection: %+v %v", d, err)
	}
	for range 2 {
		if d, err := p.Check(t.Context(), DescribeCluster, testCluster()); err != nil || !d.Allowed {
			t.Fatalf("item: %+v %v", d, err)
		}
	}
	p.evaluate = func(*cedar.PolicySet, cedar.EntityMap, cedar.Request) (cedar.Decision, cedar.Diagnostic, error) {
		return cedar.Allow, cedar.Diagnostic{}, errors.New("late failure")
	}
	d, err := p.Check(t.Context(), DescribeCluster, testCluster())
	if d.Allowed {
		t.Fatal("late error allowed")
	}
	failure := checkFailure(t, err, StageEvaluation)
	if err := attempt.Finish(OutcomeError, failure.Stage); err != nil {
		t.Fatal(err)
	}
	if err := attempt.Finish(OutcomeAllow, StageNone); err != nil {
		t.Fatal(err)
	}
	errorLabels := map[string]string{"operation": string(ListClusters), "outcome": string(OutcomeError)}
	allowLabels := map[string]string{"operation": string(ListClusters), "outcome": string(OutcomeAllow)}
	if metricSample(t, registry, "authz_requests_total", errorLabels).GetCounter().GetValue() != 1 || metricSample(t, registry, "authz_duration_seconds", errorLabels).GetHistogram().GetSampleCount() != 1 || metricSample(t, registry, "authz_requests_total", allowLabels).GetCounter().GetValue() != 0 || metricSample(t, registry, "authz_failures_total", map[string]string{"operation": string(ListClusters), "stage": string(StageEvaluation)}).GetCounter().GetValue() != 1 {
		t.Fatal("late failure did not count exactly once as error")
	}
}

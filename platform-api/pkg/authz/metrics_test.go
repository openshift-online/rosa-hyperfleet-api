package authz

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func metricSample(t *testing.T, r prometheus.Gatherer, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			match := len(metric.Label) == len(labels)
			for _, label := range metric.Label {
				if labels[label.GetName()] != label.GetValue() {
					match = false
				}
			}
			if match {
				return metric
			}
		}
	}
	return &dto.Metric{}
}

func TestMetricsAccounting(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		operation Action
		outcome   Outcome
		stage     Stage
	}{
		{"allow", ListClusters, OutcomeAllow, StageNone},
		{"deny", DescribeCluster, OutcomeDeny, StageNone},
		{"resolution", ListClusters, OutcomeError, StageResolution},
		{"parsing", DescribeCluster, OutcomeError, StageParsing},
		{"binding", ListClusters, OutcomeError, StageBinding},
		{"entities", DescribeCluster, OutcomeError, StageEntityValidation},
		{"evaluation", ListClusters, OutcomeError, StageEvaluation},
		{"resource loading", DescribeCluster, OutcomeError, StageResourceLoading},
	} {
		t.Run(tc.name, func(t *testing.T) {
			labels := map[string]string{"operation": string(tc.operation), "outcome": string(tc.outcome)}
			beforeCount := metricSample(t, registry, "authz_requests_total", labels).GetCounter().GetValue()
			beforeSamples := metricSample(t, registry, "authz_duration_seconds", labels).GetHistogram().GetSampleCount()
			failureLabels := map[string]string{"operation": string(tc.operation), "stage": string(tc.stage)}
			beforeFailures := metricSample(t, registry, "authz_failures_total", failureLabels).GetCounter().GetValue()
			attempt, err := metrics.Start(tc.operation)
			if err != nil {
				t.Fatal(err)
			}
			attempt.started = time.Now().Add(-52 * time.Millisecond)
			if err := attempt.Finish(tc.outcome, tc.stage); err != nil {
				t.Fatal(err)
			}
			if err := attempt.Finish(OutcomeDeny, StageNone); err != nil {
				t.Fatal(err)
			}
			counter := metricSample(t, registry, "authz_requests_total", labels).GetCounter().GetValue()
			histogram := metricSample(t, registry, "authz_duration_seconds", labels).GetHistogram()
			if counter-beforeCount != 1 || histogram.GetSampleCount()-beforeSamples != 1 || histogram.GetSampleSum() < 0.05 {
				t.Fatalf("bad sample delta: count=%v histogram=%+v", counter-beforeCount, histogram)
			}
			wantBuckets := []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}
			if len(histogram.Bucket) != len(wantBuckets) {
				t.Fatalf("unexpected buckets: %+v", histogram)
			}
			for i, bucket := range histogram.Bucket {
				if bucket.GetUpperBound() != wantBuckets[i] {
					t.Fatalf("bucket %d: %v", i, bucket)
				}
			}
			afterFailures := metricSample(t, registry, "authz_failures_total", failureLabels).GetCounter().GetValue()
			wantFailure := 0.0
			if tc.outcome == OutcomeError {
				wantFailure = 1
			}
			if afterFailures-beforeFailures != wantFailure {
				t.Fatalf("failure delta %v", afterFailures-beforeFailures)
			}
		})
	}
	t.Run("concurrent finalization", func(t *testing.T) {
		labels := map[string]string{"operation": string(ListClusters), "outcome": string(OutcomeAllow)}
		before := metricSample(t, registry, "authz_requests_total", labels).GetCounter().GetValue()
		attempt, err := metrics.Start(ListClusters)
		if err != nil {
			t.Fatal(err)
		}
		var group sync.WaitGroup
		for range 32 {
			group.Go(func() {
				if err := attempt.Finish(OutcomeAllow, StageNone); err != nil {
					t.Error(err)
				}
			})
		}
		group.Wait()
		if delta := metricSample(t, registry, "authz_requests_total", labels).GetCounter().GetValue() - before; delta != 1 {
			t.Fatalf("concurrent delta %v", delta)
		}
	})
}

func TestMetricsBoundedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMetrics(registry); err == nil {
		t.Fatal("duplicate registration not reported")
	}
	if attempt, err := metrics.Start(Action("/clusters/id")); err == nil || attempt != nil {
		t.Fatal("arbitrary operation admitted")
	}
	attempt, err := metrics.Start(ListClusters)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		outcome Outcome
		stage   Stage
	}{{Outcome("arbitrary"), StageNone}, {OutcomeError, Stage("secret")}, {OutcomeError, StageNone}, {OutcomeAllow, StageEvaluation}, {OutcomeDeny, StageBinding}} {
		if err := attempt.Finish(tc.outcome, tc.stage); err == nil {
			t.Fatalf("arbitrary or inconsistent labels accepted: %+v", tc)
		}
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 0 {
		t.Fatalf("invalid outcomes created samples: %+v", families)
	}
	if err := attempt.Finish(OutcomeAllow, StageNone); err != nil {
		t.Fatal(err)
	}
	isolated := prometheus.NewRegistry()
	if _, err := NewMetrics(isolated); err != nil {
		t.Fatal(err)
	}
	isolatedFamilies, err := isolated.Gather()
	if err != nil || len(isolatedFamilies) != 0 {
		t.Fatalf("registries share samples: %+v %v", isolatedFamilies, err)
	}
	if DefaultMetrics == nil {
		t.Fatal("missing default production collectors")
	}
}

func TestNoPerCheckMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}
	bundle := bundleFixture()
	bundle["policies"].([]map[string]any)[0]["content"] = labelPermit
	bundle["policies"] = append(bundle["policies"].([]map[string]any), map[string]any{"id": "list", "ownerAccountID": accountID, "content": `permit(principal, action == HyperFleet::Action::"ListClusters", resource);`})
	bundle["attachments"] = append(bundle["attachments"].([]map[string]any), map[string]any{"id": "list", "policyID": "list", "principalARN": roleARN, "bindingMode": "role-membership", "scope": "global"})
	a, err := NewAuthorizer(fixtureResolver(t, bundle))
	if err != nil {
		t.Fatal(err)
	}
	defaultBefore := authzSampleTotals(t, prometheus.DefaultGatherer)
	attempt, err := metrics.Start(ListClusters)
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.Prepare(t.Context(), testIdentity(sessionARN))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Check(t.Context(), ListClusters, testCollection()); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := p.Check(t.Context(), DescribeCluster, testCluster()); err != nil {
			t.Fatal(err)
		}
	}
	hidden := testCluster()
	hidden.Labels = nil
	if decision, err := p.Check(t.Context(), DescribeCluster, hidden); err != nil || decision.Allowed {
		t.Fatalf("hidden object did not deny: %+v %v", decision, err)
	}
	families, err := registry.Gather()
	if err != nil || len(families) != 0 {
		t.Fatalf("checks emitted metrics: %+v %v", families, err)
	}
	if !reflect.DeepEqual(defaultBefore, authzSampleTotals(t, prometheus.DefaultGatherer)) {
		t.Fatal("preparation or checks emitted default metrics")
	}
	if err := attempt.Finish(OutcomeAllow, StageNone); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"operation": string(ListClusters), "outcome": string(OutcomeAllow)}
	if metricSample(t, registry, "authz_requests_total", labels).GetCounter().GetValue() != 1 || metricSample(t, registry, "authz_duration_seconds", labels).GetHistogram().GetSampleCount() != 1 {
		t.Fatal("list did not count once")
	}
}

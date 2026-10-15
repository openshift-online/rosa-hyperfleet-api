package authz

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Outcome string

const (
	OutcomeAllow Outcome = "allow"
	OutcomeDeny  Outcome = "deny"
	OutcomeError Outcome = "error"
)

type Metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	failures *prometheus.CounterVec
}

// Attempt spans preparation, evaluation, and required resource reads, not response writes.
type Attempt struct {
	metrics   *Metrics
	operation Action
	started   time.Time
	once      sync.Once
}

// DefaultMetrics registers production collectors once, independently of server construction.
var DefaultMetrics = defaultMetrics()

// defaultMetrics registers the production collectors and panics if registration fails.
func defaultMetrics() *Metrics {
	metrics, err := NewMetrics(prometheus.DefaultRegisterer)
	if err != nil {
		panic(err)
	}
	return metrics
}

// NewMetrics registers authorization outcome, duration, and failure collectors with the supplied registry.
func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	metrics := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "authz_requests_total", Help: "Terminal authorization outcomes per admitted PoC request."}, []string{"operation", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "authz_duration_seconds", Help: "Request authorization path duration including required resource reads, excluding admission and response writes.", Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}}, []string{"operation", "outcome"}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "authz_failures_total", Help: "Requests failing authorization by first terminal failure stage."}, []string{"operation", "stage"}),
	}
	for _, collector := range []prometheus.Collector{metrics.requests, metrics.duration, metrics.failures} {
		if err := registerer.Register(collector); err != nil {
			return nil, err
		}
	}
	return metrics, nil
}

// Start begins timing one authorization operation.
func (m *Metrics) Start(operation Action) *Attempt {
	return &Attempt{metrics: m, operation: operation, started: time.Now()}
}

// Finish records the terminal outcome once; later calls are ignored.
// The stage labels failures and is ignored for allow and deny.
func (a *Attempt) Finish(outcome Outcome, stage Stage) {
	a.once.Do(func() {
		a.metrics.requests.WithLabelValues(string(a.operation), string(outcome)).Inc()
		a.metrics.duration.WithLabelValues(string(a.operation), string(outcome)).Observe(time.Since(a.started).Seconds())
		if outcome == OutcomeError {
			a.metrics.failures.WithLabelValues(string(a.operation), string(stage)).Inc()
		}
	})
}

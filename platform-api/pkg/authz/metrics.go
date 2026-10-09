package authz

import (
	"fmt"
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

type metricsCollector struct{ metrics *Metrics }

// Describe publishes descriptors for the authorization outcome, duration, and failure metrics.
func (c metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	c.metrics.requests.Describe(ch)
	c.metrics.duration.Describe(ch)
	c.metrics.failures.Describe(ch)
}

// Collect publishes the current authorization metric samples.
func (c metricsCollector) Collect(ch chan<- prometheus.Metric) {
	c.metrics.requests.Collect(ch)
	c.metrics.duration.Collect(ch)
	c.metrics.failures.Collect(ch)
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
	if registerer == nil {
		return nil, fmt.Errorf("metrics registerer is required")
	}
	metrics := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "authz_requests_total", Help: "Terminal authorization outcomes per admitted PoC request."}, []string{"operation", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "authz_duration_seconds", Help: "Request authorization path duration including required resource reads, excluding admission and response writes.", Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}}, []string{"operation", "outcome"}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "authz_failures_total", Help: "Requests failing authorization by first terminal failure stage."}, []string{"operation", "stage"}),
	}
	// One collector registration makes duplicate registration atomic.
	if err := registerer.Register(metricsCollector{metrics: metrics}); err != nil {
		return nil, err
	}
	return metrics, nil
}

// Start begins timing one supported authorization operation.
func (m *Metrics) Start(operation Action) (*Attempt, error) {
	if actionResourceKind(operation) == "" {
		return nil, fmt.Errorf("unsupported authorization operation")
	}
	return &Attempt{metrics: m, operation: operation, started: time.Now()}, nil
}

// Finish validates the terminal outcome and records metrics only for the first valid completion.
func (a *Attempt) Finish(outcome Outcome, stage Stage) error {
	switch outcome {
	case OutcomeAllow, OutcomeDeny:
		if stage != StageNone {
			return fmt.Errorf("non-error outcome must not have a failure stage")
		}
	case OutcomeError:
		switch stage {
		case StageResolution, StageParsing, StageBinding, StageEntityValidation, StageEvaluation, StageResourceLoading:
		default:
			return fmt.Errorf("error outcome requires a bounded failure stage")
		}
	default:
		return fmt.Errorf("unsupported authorization outcome")
	}
	a.once.Do(func() {
		a.metrics.requests.WithLabelValues(string(a.operation), string(outcome)).Inc()
		a.metrics.duration.WithLabelValues(string(a.operation), string(outcome)).Observe(time.Since(a.started).Seconds())
		if outcome == OutcomeError {
			a.metrics.failures.WithLabelValues(string(a.operation), string(stage)).Inc()
		}
	})
	return nil
}

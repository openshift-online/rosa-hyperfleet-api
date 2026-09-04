package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// SilenceMetrics exposes silence reconciler health signals.
type SilenceMetrics struct {
	ReconcileErrorsTotal prometheus.Counter
}

// NewSilenceMetrics registers silence reconciler metrics.
func NewSilenceMetrics(reg prometheus.Registerer) *SilenceMetrics {
	m := &SilenceMetrics{
		ReconcileErrorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "hyperfleet",
			Subsystem: "silence",
			Name:      "reconcile_errors_total",
			Help:      "Total silence reconciler failures talking to Alertmanager.",
		}),
	}
	reg.MustRegister(m.ReconcileErrorsTotal)
	return m
}

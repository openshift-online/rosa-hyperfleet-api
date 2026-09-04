package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNewSilenceMetrics(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewSilenceMetrics(reg)
	m.ReconcileErrorsTotal.Inc()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	found := false
	for _, f := range families {
		if f.GetName() == "hyperfleet_silence_reconcile_errors_total" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("hyperfleet_silence_reconcile_errors_total not found")
	}
}

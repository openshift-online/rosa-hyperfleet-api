package render

import (
	"strings"
	"testing"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

func TestPassthroughSpecsRoundTrip(t *testing.T) {
	hostedCluster, err := toHostedClusterSpec(&hyperfleetv1alpha1.HostedClusterSpecPassthrough{})
	if err != nil {
		t.Fatalf("toHostedClusterSpec: %v", err)
	}
	if hostedCluster == nil {
		t.Fatal("toHostedClusterSpec returned nil")
	}

	nodePool, err := toNodePoolSpec(&hyperfleetv1alpha1.NodePoolSpecPassthrough{})
	if err != nil {
		t.Fatalf("toNodePoolSpec: %v", err)
	}
	if nodePool == nil {
		t.Fatal("toNodePoolSpec returned nil")
	}
}

func TestRoundTripJSONReportsMarshalAndUnmarshalFailures(t *testing.T) {
	t.Run("marshal", func(t *testing.T) {
		type badSource chan int
		var target struct{}
		err := roundTripJSON(badSource(nil), &target)
		if err == nil || !strings.Contains(err.Error(), "marshaling badSource") {
			t.Fatalf("roundTripJSON marshal error = %v, want source type in error", err)
		}
	})

	t.Run("unmarshal", func(t *testing.T) {
		err := roundTripJSON(map[string]any{"field": "value"}, 0)
		if err == nil || !strings.Contains(err.Error(), "unmarshalling to int") {
			t.Fatalf("roundTripJSON unmarshal error = %v, want target type in error", err)
		}
	})
}

func TestReflectedTypeName(t *testing.T) {
	type sample struct{}
	var nilValue any
	if got := reflectedTypeName(nilValue); got != "<nil>" {
		t.Errorf("reflectedTypeName(nil) = %q, want <nil>", got)
	}

	var value *sample
	pointer := &value
	if got := reflectedTypeName(pointer); got != "sample" {
		t.Errorf("reflectedTypeName(pointer) = %q, want sample", got)
	}
}

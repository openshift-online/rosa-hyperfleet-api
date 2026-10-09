package render

import (
	"encoding/json"
	"fmt"
	"reflect"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

func toHostedClusterSpec(p *hyperfleetv1alpha1.HostedClusterSpecPassthrough) (*hypershiftv1beta1.HostedClusterSpec, error) {
	var spec hypershiftv1beta1.HostedClusterSpec
	if err := roundTripJSON(p, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

func toNodePoolSpec(p *hyperfleetv1alpha1.NodePoolSpecPassthrough) (*hypershiftv1beta1.NodePoolSpec, error) {
	var spec hypershiftv1beta1.NodePoolSpec
	if err := roundTripJSON(p, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

func roundTripJSON(source, target any) error {
	data, err := json.Marshal(source)
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", reflectedTypeName(source), err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("unmarshalling to %s: %w", reflectedTypeName(target), err)
	}
	return nil
}

func reflectedTypeName(value any) string {
	t := reflect.TypeOf(value)
	if t == nil {
		return "<nil>"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

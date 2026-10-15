package e2e_test

import (
	"fmt"
	"os"
)

func newOperatorClient(baseURL string) (*APIClient, error) {
	profile := os.Getenv("E2E_SERVICE_OPERATOR_PROFILE")
	if profile == "" {
		return nil, fmt.Errorf("E2E_SERVICE_OPERATOR_PROFILE is required for ManagementCluster tests")
	}
	client := NewAPIClient(baseURL)
	client.AWSProfile = profile
	return client, nil
}

package e2e_test

import (
	awstest "github.com/openshift-online/rosa-hyperfleet-api/test/helpers/aws"
)

// Clean these up in a future PR?
type APIClient = awstest.APIClient
type APIResponse = awstest.APIResponse

var NewAPIClient = awstest.NewAPIClient

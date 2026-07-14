package e2e_test

import (
	awstest "github.com/openshift/rosa-hyperfleet-api/internal/test/aws"
)

// Clean these up in a future PR?
type APIClient = awstest.APIClient
type APIResponse = awstest.APIResponse
type CheckAuthorizationRequest = awstest.CheckAuthorizationRequest

var NewAPIClient = awstest.NewAPIClient
var SeedAdminDirect = awstest.SeedAdminDirect

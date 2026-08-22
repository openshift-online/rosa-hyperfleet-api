package authzlocal_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAuthzLocal(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Local Authz E2E Suite")
}

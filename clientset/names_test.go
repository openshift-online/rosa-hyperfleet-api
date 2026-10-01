package hyperfleet

import "testing"

func TestNames(t *testing.T) {
	if got := NodePoolName("prod", "workers"); got != "prod.workers" {
		t.Errorf("NodePoolName = %q", got)
	}
	if got := AccountNamespace("123456789012"); got != "account-123456789012" {
		t.Errorf("AccountNamespace = %q", got)
	}
	if got := ClusterUIDSelector("abc"); got != "hyperfleet.io/cluster-uid=abc" {
		t.Errorf("ClusterUIDSelector = %q", got)
	}
}

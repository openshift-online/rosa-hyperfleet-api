package e2e_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type operatorTransport struct {
	signature *string
}

func (transport operatorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	*transport.signature = request.Header.Get("Authorization")
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

func TestOperatorClientRequired(t *testing.T) {
	t.Setenv("E2E_SERVICE_OPERATOR_PROFILE", "")
	if _, err := newOperatorClient("https://example.invalid"); err == nil {
		t.Fatal("missing operator profile must not fall back to customer credentials")
	}
}

func TestOperatorClientSigner(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials")
	if err := os.WriteFile(credentials, []byte("[customer]\naws_access_key_id=customer-key\naws_secret_access_key=customer-secret\n[operator]\naws_access_key_id=operator-key\naws_secret_access_key=operator-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentials)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "absent-config"))
	t.Setenv("AWS_PROFILE", "customer")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("E2E_SERVICE_OPERATOR_PROFILE", "operator")
	var signature string
	previous := http.DefaultTransport
	http.DefaultTransport = operatorTransport{signature: &signature}
	t.Cleanup(func() { http.DefaultTransport = previous })
	const baseURL = "https://test.execute-api.us-east-1.amazonaws.com"
	operator, err := newOperatorClient(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		client *APIClient
		key    string
	}{
		{operator, "operator-key"},
		{NewAPIClient(baseURL), "customer-key"},
	} {
		if _, err := tc.client.Get("/api/v0/management_clusters", "123456789012"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(signature, "Credential="+tc.key+"/") {
			t.Fatalf("request was not signed by %s: %s", tc.key, signature)
		}
	}
}

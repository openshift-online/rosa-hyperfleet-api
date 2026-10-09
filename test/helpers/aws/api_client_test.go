package aws

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIGatewayRegionFromURL(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		region string
	}{
		{"execute east", "https://abc123.execute-api.us-east-1.amazonaws.com/prod", "us-east-1"},
		{"execute west", "https://abc123.execute-api.us-west-2.amazonaws.com", "us-west-2"},
		{"execute europe", "https://abc123.execute-api.eu-west-2.amazonaws.com/stage/api/v0", "eu-west-2"},
		{"execute multi-part region", "https://abc123.execute-api.ap-southeast-3.amazonaws.com", "ap-southeast-3"},
		{"execute gov", "https://abc123.execute-api.us-gov-west-1.amazonaws.com", "us-gov-west-1"},
		{"execute china", "https://abc123.execute-api.cn-north-1.amazonaws.com.cn/prod", "cn-north-1"},
		{"execute china northwest", "https://abc123.execute-api.cn-northwest-1.amazonaws.com.cn", "cn-northwest-1"},
		{"execute partition mismatch", "https://abc123.execute-api.us-east-1.amazonaws.com.cn", ""},
		{"execute china wrong suffix", "https://abc123.execute-api.cn-north-1.amazonaws.com", ""},
		{"execute port", "http://abc123.execute-api.eu-central-1.amazonaws.com:8080", "eu-central-1"},
		{"execute uppercase", "https://ABC123.EXECUTE-API.US-WEST-2.AMAZONAWS.COM", "us-west-2"},
		{"execute trailing dot", "https://abc123.execute-api.us-west-2.amazonaws.com./prod", "us-west-2"},
		{"execute missing id", "https://execute-api.us-east-1.amazonaws.com", ""},
		{"execute missing region", "https://abc123.execute-api.amazonaws.com", ""},
		{"execute invalid region", "https://abc123.execute-api.us-east.amazonaws.com", ""},
		{"execute suffix attack", "https://abc123.execute-api.us-east-1.amazonaws.com.evil.test", ""},
		{"execute prefix attack", "https://evil.abc123.execute-api.us-east-1.amazonaws.com", ""},
		{"execute lookalike suffix", "https://abc123.execute-api.us-east-1.notamazonaws.com", ""},
		{"execute lookalike service", "https://abc123.notexecute-api.us-east-1.amazonaws.com", ""},
		{"execute other service", "https://ec2.us-east-1.amazonaws.com", ""},
		{"rosa normal", "https://api.us-east-1.int0.rosa.devshift.net", "us-east-1"},
		{"rosa west", "https://api.us-west-2.dev0.rosa.devshift.net/api/v0", "us-west-2"},
		{"rosa stage", "https://api.us-east-1.stg0.rosa.devshift.org", "us-east-1"},
		{"rosa stage suffix attack", "https://api.us-east-1.stg0.rosa.devshift.org.evil.test", ""},
		{"rosa europe", "https://api.eu-central-1.int0.rosa.devshift.net", "eu-central-1"},
		{"rosa gov", "https://api.us-gov-east-1.int0.rosa.devshift.net", "us-gov-east-1"},
		{"rosa china", "https://api.cn-northwest-1.int0.rosa.devshift.net", "cn-northwest-1"},
		{"rosa ephemeral", "https://api.us-east-1-xg4y.dev0.rosa.devshift.net", "us-east-1"},
		{"rosa ephemeral ci", "https://api.eu-west-2-pr-123.ci00.rosa.devshift.net", "eu-west-2"},
		{"rosa ephemeral gov", "https://api.us-gov-west-1-pr-123.ci00.rosa.devshift.net", "us-gov-west-1"},
		{"rosa ephemeral china", "https://api.cn-north-1-xg4y.dev0.rosa.devshift.net", "cn-north-1"},
		{"rosa uppercase port", "https://API.US-WEST-2-XG4Y.DEV0.ROSA.DEVSHIFT.NET:443", "us-west-2"},
		{"rosa trailing dot", "https://api.eu-west-2.int0.rosa.devshift.net.", "eu-west-2"},
		{"rosa environment hyphen", "https://api.us-west-2.dev-0.rosa.devshift.net", "us-west-2"},
		{"rosa suffix attack", "https://api.us-east-1.int0.rosa.devshift.net.evil.test", ""},
		{"rosa lookalike suffix", "https://api.us-east-1.int0.rosa.devshiftXnet", ""},
		{"rosa prefix attack", "https://evil.api.us-east-1.int0.rosa.devshift.net", ""},
		{"rosa wrong service", "https://thanos.us-east-1.int0.rosa.devshift.net", ""},
		{"rosa missing environment", "https://api.us-east-1.rosa.devshift.net", ""},
		{"rosa extra environment", "https://api.us-east-1.dev.int0.rosa.devshift.net", ""},
		{"rosa invalid region", "https://api.us-east.int0.rosa.devshift.net", ""},
		{"rosa empty prefix", "https://api.us-east-1-.int0.rosa.devshift.net", ""},
		{"rosa invalid prefix", "https://api.us-east-1-_bad.int0.rosa.devshift.net", ""},
		{"rosa empty environment", "https://api.us-east-1..rosa.devshift.net", ""},
		{"rosa invalid environment", "https://api.us-east-1.-int0.rosa.devshift.net", ""},
		{"localhost", "http://localhost:8080", ""},
		{"ipv4", "http://127.0.0.1:8080", ""},
		{"ipv4 loopback range", "http://127.0.0.2:8080", ""},
		{"ipv6", "http://[::1]:8080", ""},
		{"ipv6 expanded", "http://[0:0:0:0:0:0:0:1]:8080", ""},
		{"ipv4 mapped ipv6", "http://[::ffff:127.0.0.1]:8080", ""},
		{"monitoring", "https://thanos.example.test/api/v1/query", ""},
		{"monitoring rosa", "https://rhobs.us-east-1.int0.rosa.devshift.net", ""},
		{"path confusion", "https://example.test/api.us-east-1.int0.rosa.devshift.net", ""},
		{"query confusion", "https://example.test/?url=https://abc123.execute-api.us-east-1.amazonaws.com", ""},
		{"fragment confusion", "https://example.test/#https://api.us-east-1.int0.rosa.devshift.net", ""},
		{"userinfo confusion", "https://api.us-east-1.int0.rosa.devshift.net@evil.test", ""},
		{"execute userinfo confusion", "https://abc123.execute-api.us-east-1.amazonaws.com@evil.test", ""},
		{"host overrides path", "https://abc123.execute-api.eu-west-2.amazonaws.com/api.us-east-1.int0.rosa.devshift.net", "eu-west-2"},
		{"localhost query confusion", "http://localhost:8080/?url=https://api.us-east-1.int0.rosa.devshift.net", ""},
		{"missing scheme", "api.us-east-1.int0.rosa.devshift.net", ""},
		{"scheme relative", "//api.us-east-1.int0.rosa.devshift.net", ""},
		{"non-http scheme", "ftp://api.us-east-1.int0.rosa.devshift.net", ""},
		{"malformed", "https://[::1", ""},
		{"escaped host", "https://api.us-east-1.int0.rosa.devshift.net%2eevil.test", ""},
		{"invalid port", "https://api.us-east-1.int0.rosa.devshift.net:bad", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := apiGatewayRegionFromURL(tc.url); got != tc.region {
				t.Errorf("region = %q, want %q", got, tc.region)
			}
		})
	}
}

func isolateAWSConfig(t *testing.T) string {
	t.Helper()
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if strings.HasPrefix(key, "AWS_") {
			t.Setenv(key, "")
		}
	}
	dir := t.TempDir()
	for key, name := range map[string]string{
		"AWS_CONFIG_FILE":             "config",
		"AWS_SHARED_CREDENTIALS_FILE": "credentials",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REGION", "us-east-1")
	return dir
}

func TestLocalRequestsUnsigned(t *testing.T) {
	isolateAWSConfig(t)
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			requests := make(chan *http.Request, 1)
			bodies := make(chan string, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				requests <- r.Clone(r.Context())
				bodies <- string(body)
				w.Header().Set("X-Test-Response", "local")
				_, _ = io.WriteString(w, `{"status":"ok"}`)
			}))
			if host == "::1" {
				listener, err := net.Listen("tcp6", "[::1]:0")
				if err != nil {
					server.Close()
					t.Skipf("IPv6 loopback unavailable: %v", err)
				}
				_ = server.Listener.Close()
				server.Listener = listener
			}
			server.Start()
			defer server.Close()
			base, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			base.Host = net.JoinHostPort(host, base.Port())
			client := NewAPIClient(base.String())
			client.CallerARN = "arn:aws:iam::123456789012:role/local-reader"

			for _, method := range []string{http.MethodGet, http.MethodPatch} {
				t.Run(method, func(t *testing.T) {
					var resp *APIResponse
					var err error
					wantBody := ""
					if method == http.MethodPatch {
						wantBody = `{"spec":{"compute_replicas":3}}`
						resp, err = client.Patch("/api/v0/clusters/test-id", map[string]any{
							"spec": map[string]any{"compute_replicas": 3},
						}, "123456789012")
					} else {
						resp, err = client.Do(method, "/api/v0/clusters/test-id", nil, "123456789012")
					}
					if err != nil {
						t.Fatalf("unsigned local request failed: %v", err)
					}
					if resp.StatusCode != http.StatusOK || string(resp.Body) != `{"status":"ok"}` || resp.Headers.Get("X-Test-Response") != "local" {
						t.Errorf("unexpected local response: %+v", resp)
					}
					req := <-requests
					body := <-bodies
					if req.Method != method || req.URL.Path != "/api/v0/clusters/test-id" || body != wantBody {
						t.Error("request method, path, or body changed")
					}
					for key, want := range map[string]string{
						"Content-Type":         "application/json",
						"X-Amz-Account-Id":     "123456789012",
						"X-Amz-Caller-Arn":     "arn:aws:iam::123456789012:role/local-reader",
						"Authorization":        "",
						"X-Amz-Date":           "",
						"X-Amz-Security-Token": "",
					} {
						if got := req.Header.Get(key); got != want {
							t.Errorf("%s = %q, want %q", key, got, want)
						}
					}
				})
			}
		})
	}
}

type captureTransport func(*http.Request) (*http.Response, error)

func (f captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMonitoringRequestsUnsigned(t *testing.T) {
	isolateAWSConfig(t)
	client := NewAPIClient("https://thanos.us-west-2.int0.rosa.devshift.net")
	client.AWSProfile = "absent-profile"
	wasSent := false
	client.httpClient.Transport = captureTransport(func(req *http.Request) (*http.Response, error) {
		wasSent = true
		if req.Header.Get("Authorization") != "" || req.Header.Get("X-Amz-Date") != "" {
			t.Error("non-API monitoring request was signed")
		}
		if req.URL.Path != "/api/v1/query" || req.URL.Query().Get("query") != "up" {
			t.Error("monitoring query changed")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":"success"}`)), Header: make(http.Header)}, nil
	})
	resp, err := client.Get("/api/v1/query?query=up", "")
	if err != nil {
		t.Fatalf("unsigned monitoring request failed: %v", err)
	}
	if !wasSent || resp.StatusCode != http.StatusOK || string(resp.Body) != `{"status":"success"}` {
		t.Error("monitoring response was not delivered")
	}
}

func TestDeployedRequestsSigned(t *testing.T) {
	dir := isolateAWSConfig(t)
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(`[default]
aws_access_key_id = DEFAULTTESTKEY
aws_secret_access_key = default-test-secret
aws_session_token = default-test-token
[customer]
aws_access_key_id = CUSTOMERTESTKEY
aws_secret_access_key = customer-test-secret
aws_session_token = customer-test-token
`), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		url     string
		region  string
		profile string
		key     string
		token   string
	}{
		{"execute default", "https://abc123.execute-api.us-west-2.amazonaws.com/prod", "us-west-2", "", "DEFAULTTESTKEY", "default-test-token"},
		{"execute profile", "https://abc123.execute-api.eu-west-2.amazonaws.com", "eu-west-2", "customer", "CUSTOMERTESTKEY", "customer-test-token"},
		{"execute gov", "https://abc123.execute-api.us-gov-west-1.amazonaws.com", "us-gov-west-1", "customer", "CUSTOMERTESTKEY", "customer-test-token"},
		{"execute china", "https://abc123.execute-api.cn-north-1.amazonaws.com.cn", "cn-north-1", "", "DEFAULTTESTKEY", "default-test-token"},
		{"rosa normal", "https://api.eu-central-1.int0.rosa.devshift.net", "eu-central-1", "", "DEFAULTTESTKEY", "default-test-token"},
		{"rosa stage", "https://api.us-east-1.stg0.rosa.devshift.org", "us-east-1", "customer", "CUSTOMERTESTKEY", "customer-test-token"},
		{"rosa ephemeral", "https://api.us-west-2-pr-123.ci00.rosa.devshift.net", "us-west-2", "customer", "CUSTOMERTESTKEY", "customer-test-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := NewAPIClient(tc.url)
			client.AWSProfile = tc.profile
			client.CallerARN = "arn:aws:iam::123456789012:role/test-reader"
			for _, method := range []string{http.MethodGet, http.MethodPatch} {
				t.Run(method, func(t *testing.T) {
					var body any
					wantBody := ""
					if method == http.MethodPatch {
						body = map[string]any{"name": "test"}
						wantBody = `{"name":"test"}`
					}
					wasSent := false
					client.httpClient.Transport = captureTransport(func(req *http.Request) (*http.Response, error) {
						wasSent = true
						payload := ""
						if req.Body != nil {
							content, err := io.ReadAll(req.Body)
							if err != nil {
								t.Error(err)
							}
							payload = string(content)
						}
						if req.Method != method || req.URL.String() != tc.url+"/api/v0/clusters/test-id" || payload != wantBody {
							t.Error("signed request method, URL, or body changed")
						}
						auth := req.Header.Get("Authorization")
						if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+tc.key+"/") || !strings.Contains(auth, "/"+tc.region+"/execute-api/aws4_request,") || !strings.Contains(auth, "Signature=") {
							t.Error("SigV4 credential, region, service, or signature missing or incorrect")
						}
						if req.Header.Get("X-Amz-Date") == "" || req.Header.Get("X-Amz-Security-Token") != tc.token {
							t.Error("SigV4 date or session token missing or incorrect")
						}
						if req.Header.Get("X-Amz-Account-Id") != "123456789012" || req.Header.Get("X-Amz-Caller-Arn") != client.CallerARN || req.Header.Get("Content-Type") != "application/json" {
							t.Error("identity or content-type headers changed")
						}
						return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":"ok"}`)), Header: make(http.Header)}, nil
					})
					resp, err := client.Do(method, "/api/v0/clusters/test-id", body, "123456789012")
					if err != nil {
						t.Fatal(err)
					}
					if !wasSent || resp.StatusCode != http.StatusOK {
						t.Error("signed request was not delivered")
					}
				})
			}
		})
	}
}

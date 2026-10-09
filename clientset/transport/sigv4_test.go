/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package transport

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// captureTransport records the forwarded request and returns a minimal 200 response.
type captureTransport struct {
	got *http.Request
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		clone.Body = io.NopCloser(strings.NewReader(string(b)))
	}
	c.got = clone
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func staticConfig() aws.Config {
	return aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKIATEST", "secretkey", "sessiontoken"),
	}
}

func TestSigV4_AddsAuthorizationHeader(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "acct", "arn:aws:iam::123:user/test")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/api/v0/clusters", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if cap.got.Header.Get("Authorization") == "" {
		t.Error("Authorization header missing — request was not signed")
	}
}

func TestSigV4_AuthorizationUsesExecuteAPIService(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "acct", "arn")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/api/v0/clusters", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	auth := cap.got.Header.Get("Authorization")
	if !strings.Contains(auth, "execute-api") {
		t.Errorf("Authorization header does not contain execute-api service name: %s", auth)
	}
}

func TestSigV4_AccountIDIsInSignedHeaders(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "acct", "arn")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/api/v0/clusters", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if !inSignedHeaders(cap.got.Header.Get("Authorization"), strings.ToLower(headerAccountID)) {
		t.Errorf("%s not found in SignedHeaders of Authorization header", headerAccountID)
	}
}

func TestSigV4_CallerARNIsInSignedHeaders(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "acct", "arn:aws:iam::123:user/caller")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/api/v0/clusters", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if !inSignedHeaders(cap.got.Header.Get("Authorization"), strings.ToLower(headerCallerARN)) {
		t.Errorf("%s not found in SignedHeaders of Authorization header", headerCallerARN)
	}
}

// inSignedHeaders parses the SignedHeaders list from a SigV4 Authorization
// header and reports whether name appears in it.
// Authorization format: AWS4-HMAC-SHA256 Credential=..., SignedHeaders=a;b;c, Signature=...
func inSignedHeaders(authHeader, name string) bool {
	for _, part := range strings.Split(authHeader, ", ") {
		if strings.HasPrefix(part, "SignedHeaders=") {
			signed := strings.TrimPrefix(part, "SignedHeaders=")
			for _, h := range strings.Split(signed, ";") {
				if h == name {
					return true
				}
			}
		}
	}
	return false
}

func TestSigV4_SetsAccountIDHeader(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "my-account-id", "arn")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/api/v0/clusters", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := cap.got.Header.Get(headerAccountID); got != "my-account-id" {
		t.Errorf("%s = %q, want my-account-id", headerAccountID, got)
	}
}

func TestSigV4_SetsCallerARNHeader(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "acct", "arn:aws:iam::123:user/caller")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/api/v0/clusters", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := cap.got.Header.Get(headerCallerARN); got != "arn:aws:iam::123:user/caller" {
		t.Errorf("%s = %q, want arn:aws:iam::123:user/caller", headerCallerARN, got)
	}
}

func TestSigV4_NamespaceSegmentStrippedFromURL(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "default", "arn")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/api/v0/namespaces/acct-999/clusters", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if cap.got.URL.Path != "/api/v0/clusters" {
		t.Errorf("path = %q, want /api/v0/clusters", cap.got.URL.Path)
	}
}

func TestSigV4_NamespaceDoesNotOverrideConfiguredAccountID(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "default-acct", "arn")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/api/v0/namespaces/override-acct/clusters", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := cap.got.Header.Get(headerAccountID); got != "default-acct" {
		t.Errorf("%s = %q, want default-acct", headerAccountID, got)
	}
}

func TestSigV4_BodyPreservedAfterSigning(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "acct", "arn")

	body := `{"spec":{"foo":"bar"}}`
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/api/v0/clusters",
		io.NopCloser(strings.NewReader(body)))
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	got, _ := io.ReadAll(cap.got.Body)
	if string(got) != body {
		t.Errorf("body = %q, want %q", string(got), body)
	}
}

func TestSigV4_NilBodyDoesNotError(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "acct", "arn")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/api/v0/clusters", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip with nil body: %v", err)
	}
}

func TestSigV4_OriginalRequestNotMutated(t *testing.T) {
	cap := &captureTransport{}
	rt := New(cap, staticConfig(), "us-east-1", "acct", "arn")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/api/v0/namespaces/acct-999/clusters", nil)
	originalPath := req.URL.Path

	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if req.URL.Path != originalPath {
		t.Errorf("caller's request path was mutated: got %q, want %q", req.URL.Path, originalPath)
	}
}

func TestSigV4_NilInnerDefaultsToHTTPDefaultTransport(t *testing.T) {
	rt := New(nil, staticConfig(), "us-east-1", "acct", "arn")
	if rt.inner != http.DefaultTransport {
		t.Error("expected inner to be http.DefaultTransport when nil is passed")
	}
}

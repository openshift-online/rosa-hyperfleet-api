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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAdapter() *Adapter {
	return NewAdapter(http.DefaultTransport)
}

func getRequest(rawURL string) *http.Request {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		panic(err)
	}
	return req
}

func postRequest(rawURL, body string) *http.Request {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, rawURL, io.NopCloser(strings.NewReader(body)))
	if err != nil {
		panic(err)
	}
	return req
}

// testServer starts a handler and returns the adapter and server URL.
func testServer(t *testing.T, handler http.HandlerFunc) (*Adapter, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewAdapter(http.DefaultTransport), srv.URL
}

// --- RoundTrip: request body passthrough ---

func TestRoundTrip_KubernetesNativeBodyPassesThrough(t *testing.T) {
	body := `{"metadata":{"name":"my-cluster","uid":"uid-1"},"spec":{"foo":"bar"}}`
	var capturedBody string

	a, srvURL := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		w.WriteHeader(http.StatusOK)
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srvURL+"/api/v0/clusters", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := a.RoundTrip(req)
	if resp != nil {
		defer func() {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("resp.Body.Close: %v", cerr)
			}
		}()
	}
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if capturedBody != body {
		t.Errorf("body changed: got %q, want %q", capturedBody, body)
	}
}

// --- RoundTrip: response passthrough ---

func TestRoundTrip_SuccessResponsePassesThrough(t *testing.T) {
	apiBody := `{"apiVersion":"hyperfleet.io/v1alpha1","kind":"Cluster","metadata":{"name":"my-cluster","uid":"uid-1"},"spec":{"foo":"bar"}}`

	a, srvURL := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(apiBody))
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srvURL+"/api/v0/clusters/uid-1", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := a.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("resp.Body.Close: %v", err)
		}
	}()

	b, _ := io.ReadAll(resp.Body)
	if string(b) != apiBody {
		t.Errorf("response body changed: got %q, want %q", string(b), apiBody)
	}
}

func TestRoundTrip_NativeMetav1StatusPassesThrough(t *testing.T) {
	statusBody := `{"apiVersion":"v1","kind":"Status","status":"Failure","message":"CLUSTERS-MGMT-409: cluster already exists","reason":"Conflict","code":409}`

	a, srvURL := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(statusBody))
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srvURL+"/api/v0/clusters", strings.NewReader(`{"metadata":{"name":"x"},"spec":{}}`))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := a.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("resp.Body.Close: %v", err)
		}
	}()

	b, _ := io.ReadAll(resp.Body)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("body is not JSON: %v — body: %s", err, b)
	}
	if string(m["kind"]) != `"Status"` {
		t.Errorf("kind = %s, want \"Status\"", m["kind"])
	}
	if string(m["reason"]) != `"Conflict"` {
		t.Errorf("reason = %s, want \"Conflict\"", m["reason"])
	}
	if !strings.Contains(string(m["message"]), "CLUSTERS-MGMT-409") {
		t.Errorf("message missing code: %s", m["message"])
	}
}

// --- adaptListQuery ---

func TestAdaptListQuery_NumericContinueRewrittenToOffset(t *testing.T) {
	a := newAdapter()
	req := getRequest("https://example.com/api/v0/clusters?continue=50")

	out := a.adaptListQuery(req)
	q := out.URL.Query()
	if q.Get("offset") != "50" {
		t.Errorf("offset = %q, want 50", q.Get("offset"))
	}
	if q.Get("continue") != "" {
		t.Errorf("continue should be removed, got %q", q.Get("continue"))
	}
}

func TestAdaptListQuery_NonNumericContinuePassesThrough(t *testing.T) {
	a := newAdapter()
	req := getRequest("https://example.com/api/v0/clusters?continue=cursor-token")

	out := a.adaptListQuery(req)
	q := out.URL.Query()
	if q.Get("continue") != "cursor-token" {
		t.Errorf("continue changed: got %q, want cursor-token", q.Get("continue"))
	}
	if q.Get("offset") != "" {
		t.Errorf("offset should be absent, got %q", q.Get("offset"))
	}
}

func TestAdaptListQuery_NoContinuePassesThrough(t *testing.T) {
	a := newAdapter()
	req := getRequest("https://example.com/api/v0/clusters")

	out := a.adaptListQuery(req)
	if out.URL.RawQuery != "" {
		t.Errorf("query changed: got %q, want empty", out.URL.RawQuery)
	}
}

func TestAdaptListQuery_NonGETNotRewritten(t *testing.T) {
	a := newAdapter()
	req := postRequest("https://example.com/api/v0/clusters?continue=50", `{}`)

	out := a.adaptListQuery(req)
	q := out.URL.Query()
	if q.Get("offset") != "" {
		t.Error("offset should not be set on non-GET request")
	}
	if q.Get("continue") != "50" {
		t.Errorf("continue should be preserved on non-GET: got %q", q.Get("continue"))
	}
}

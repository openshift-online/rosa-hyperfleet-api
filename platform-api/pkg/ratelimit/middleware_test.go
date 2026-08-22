package ratelimit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

func setupTest(t *testing.T, cfg *Config) (*Limiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		if err := rdb.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	})
	logger := slog.Default()
	return New(NewRedisLimiter(rdb), cfg, logger), mr
}

func defaultTestConfig() *Config {
	cfg := &Config{
		Enabled:      true,
		RedisTimeout: 1000,
		Default:      RouteLimit{Rate: 5, Burst: 5, Window: 1},
		exemptSet:    map[string]struct{}{},
	}
	return cfg
}

func reqWithAccount(method, path, accountID string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	if accountID != "" {
		ctx := context.WithValue(r.Context(), middleware.ContextKeyAccountID, accountID)
		r = r.WithContext(ctx)
	}
	return r
}

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestMiddleware_SetsRateLimitHeadersOnAllowedRequests(t *testing.T) {
	lim, _ := setupTest(t, defaultTestConfig())
	handler := lim.Middleware(okHandler)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-RateLimit-Limit") != "5" {
		t.Errorf("expected X-RateLimit-Limit=5, got %s", w.Header().Get("X-RateLimit-Limit"))
	}
	if w.Header().Get("X-RateLimit-Remaining") == "" {
		t.Error("expected X-RateLimit-Remaining header on allowed request")
	}
	if w.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("expected X-RateLimit-Reset header on allowed request")
	}
}

func TestMiddleware_AllowsRequestsUnderLimit(t *testing.T) {
	lim, _ := setupTest(t, defaultTestConfig())
	handler := lim.Middleware(okHandler)

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, w.Code)
		}
	}
}

func TestMiddleware_DeniesRequestsOverLimit(t *testing.T) {
	lim, _ := setupTest(t, defaultTestConfig())
	handler := lim.Middleware(okHandler)

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestMiddleware_IsolatesRateLimitsByAccount(t *testing.T) {
	lim, _ := setupTest(t, defaultTestConfig())
	handler := lim.Middleware(okHandler)

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-2"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for different account, got %d", w.Code)
	}
}

func TestMiddleware_SkipsWhenNoAccountID(t *testing.T) {
	lim, _ := setupTest(t, defaultTestConfig())
	handler := lim.Middleware(okHandler)

	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", ""))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200 with no account ID, got %d", i, w.Code)
		}
	}
}

func TestMiddleware_SkipsExemptAccounts(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.ExemptAccounts = []string{"exempt-acct"}
	cfg.exemptSet = map[string]struct{}{"exempt-acct": {}}

	lim, _ := setupTest(t, cfg)
	handler := lim.Middleware(okHandler)

	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "exempt-acct"))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200 for exempt account, got %d", i, w.Code)
		}
	}

	// Non-exempt account should be rate limited with the same config
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "regular-acct"))
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "regular-acct"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for non-exempt account, got %d", w.Code)
	}
}

func TestMiddleware_AppliesRouteOverrides(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Routes = []RouteLimit{
		{Path: "/api/v0/clusters", Method: "POST", Rate: 2, Burst: 2, Window: 60},
	}

	lim, _ := setupTest(t, cfg)
	handler := lim.Middleware(okHandler)

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("POST", "/api/v0/clusters", "acct-1"))
		if w.Code != http.StatusOK {
			t.Fatalf("POST request %d: expected 200, got %d", i, w.Code)
		}
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("POST", "/api/v0/clusters", "acct-1"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("POST request 3: expected 429, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	if w.Code != http.StatusOK {
		t.Fatal("GET should use default limit, not POST override")
	}
}

func TestMiddleware_DifferentiatesHTTPMethods(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Default = RouteLimit{Rate: 2, Burst: 2, Window: 1}

	lim, _ := setupTest(t, cfg)
	handler := lim.Middleware(okHandler)

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatal("GET should be rate limited")
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("POST", "/api/v0/clusters", "acct-1"))
	if w.Code != http.StatusOK {
		t.Fatal("POST should have its own limit bucket")
	}
}

func TestMiddleware_FailOpenWhenRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		if err := rdb.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	})

	lim := New(NewRedisLimiter(rdb), defaultTestConfig(), slog.Default())
	handler := lim.Middleware(okHandler)

	mr.Close()

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected fail-open 200, got %d", w.Code)
	}
}

func TestMiddleware_RecoverAfterWindow(t *testing.T) {
	lim, mr := setupTest(t, defaultTestConfig())
	handler := lim.Middleware(okHandler)

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatal("should be rate limited")
	}

	mr.FastForward(2e9)

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after window reset, got %d", w.Code)
	}
}

func TestMiddleware_DisabledConfig(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Enabled = false

	lim, _ := setupTest(t, cfg)
	handler := lim.Middleware(okHandler)

	for i := 0; i < 20; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200 when disabled, got %d", i, w.Code)
		}
	}
}

func TestMiddleware_429ResponseFormat(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Default = RouteLimit{Rate: 1, Burst: 1, Window: 1}

	lim, _ := setupTest(t, cfg)
	handler := lim.Middleware(okHandler)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
	if w.Header().Get("X-RateLimit-Limit") != "1" {
		t.Errorf("expected X-RateLimit-Limit=1, got %s", w.Header().Get("X-RateLimit-Limit"))
	}
	if w.Header().Get("X-RateLimit-Remaining") == "" {
		t.Error("expected X-RateLimit-Remaining header")
	}
	if w.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("expected X-RateLimit-Reset header")
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	if body["kind"] != "Status" {
		t.Errorf("expected kind=Status, got %v", body["kind"])
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, errRateLimit.Code) {
		t.Errorf("expected message to contain code %s, got %q", errRateLimit.Code, msg)
	}
	// reason field is now the metav1.StatusReason category, not the message text
	if body["reason"] != "TooManyRequests" {
		t.Errorf("expected reason=TooManyRequests, got %v", body["reason"])
	}
	if _, hasDetails := body["details"]; hasDetails {
		t.Error("response should not contain details object")
	}
}

func TestMiddleware_KeyStructure(t *testing.T) {
	lim, mr := setupTest(t, defaultTestConfig())
	handler := lim.Middleware(okHandler)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))

	keys := mr.Keys()
	if len(keys) == 0 {
		t.Fatal("expected at least one Redis key")
	}

	found := false
	for _, k := range keys {
		if k == "rate:rl:acct-1:GET:default" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected key 'rate:rl:acct-1:GET:default', got keys: %v", keys)
	}
}

func TestMiddleware_ConcurrentRequests(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Default = RouteLimit{Rate: 10, Burst: 10, Window: 1}

	lim, _ := setupTest(t, cfg)
	handler := lim.Middleware(okHandler)

	var allowed atomic.Int32
	var denied atomic.Int32
	total := 20

	var wg sync.WaitGroup
	wg.Add(total)
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "acct-1"))
			switch w.Code {
			case http.StatusOK:
				allowed.Add(1)
			case http.StatusTooManyRequests:
				denied.Add(1)
			}
		}()
	}
	wg.Wait()

	a := int(allowed.Load())
	d := int(denied.Load())
	if a+d != total {
		t.Errorf("allowed(%d) + denied(%d) != total(%d)", a, d, total)
	}
	if d == 0 {
		t.Error("expected at least some requests to be denied")
	}
}

func getCounter(method, path, result string) float64 {
	return testutil.ToFloat64(requestsTotal.With(prometheus.Labels{
		"method": method,
		"path":   path,
		"result": result,
	}))
}

func TestMiddleware_PrometheusMetrics_Allowed(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		RedisTimeout: 10,
		Default:      RouteLimit{Rate: 10, Burst: 10, Window: 1},
		exemptSet:    map[string]struct{}{},
	}
	lim, _ := setupTest(t, cfg)
	handler := lim.Middleware(okHandler)

	before := getCounter("GET", "default", "ok")

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "metrics-acct-1"))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, w.Code)
		}
	}

	after := getCounter("GET", "default", "ok")
	delta := after - before
	if delta != 3 {
		t.Errorf("expected ratelimit_requests_total{result=allowed} to increase by 3, got %.0f", delta)
	}
}

func TestMiddleware_PrometheusMetrics_Denied(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		RedisTimeout: 100,
		Default:      RouteLimit{Rate: 1, Burst: 1, Window: 60},
		exemptSet:    map[string]struct{}{},
	}
	lim, _ := setupTest(t, cfg)
	handler := lim.Middleware(okHandler)

	beforeAllowed := getCounter("POST", "default", "ok")
	beforeDenied := getCounter("POST", "default", "over_limit")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("POST", "/api/v0/clusters", "metrics-acct-2"))
	if w.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("POST", "/api/v0/clusters", "metrics-acct-2"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", w.Code)
	}

	allowedDelta := getCounter("POST", "default", "ok") - beforeAllowed
	deniedDelta := getCounter("POST", "default", "over_limit") - beforeDenied

	if allowedDelta != 1 {
		t.Errorf("expected 1 allowed metric, got %.0f", allowedDelta)
	}
	if deniedDelta != 1 {
		t.Errorf("expected 1 denied metric, got %.0f", deniedDelta)
	}
}

func TestMiddleware_PrometheusMetrics_ErrorAllowed(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		if err := rdb.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	})

	cfg := &Config{
		Enabled:      true,
		RedisTimeout: 10,
		Default:      RouteLimit{Rate: 5, Burst: 5, Window: 1},
		exemptSet:    map[string]struct{}{},
	}
	lim := New(NewRedisLimiter(rdb), cfg, slog.Default())
	handler := lim.Middleware(okHandler)

	mr.Close()

	before := getCounter("GET", "default", "failure_mode_allowed")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "metrics-acct-3"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected fail-open 200, got %d", w.Code)
	}

	after := getCounter("GET", "default", "failure_mode_allowed")
	delta := after - before
	if delta != 1 {
		t.Errorf("expected ratelimit_requests_total{result=error_allowed} to increase by 1, got %.0f", delta)
	}
}

func TestMiddleware_PrometheusMetrics_RouteLabel(t *testing.T) {
	cfg := &Config{
		Enabled:      true,
		RedisTimeout: 10,
		Default:      RouteLimit{Rate: 10, Burst: 10, Window: 1},
		Routes: []RouteLimit{
			{Path: "/api/v0/clusters", Method: "POST", Rate: 10, Burst: 10, Window: 60},
		},
		exemptSet: map[string]struct{}{},
	}
	lim, _ := setupTest(t, cfg)
	handler := lim.Middleware(okHandler)

	beforeRoute := getCounter("POST", "/api/v0/clusters", "ok")
	beforeDefault := getCounter("GET", "default", "ok")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("POST", "/api/v0/clusters", "metrics-acct-4"))

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, reqWithAccount("GET", "/api/v0/clusters", "metrics-acct-4"))

	routeDelta := getCounter("POST", "/api/v0/clusters", "ok") - beforeRoute
	defaultDelta := getCounter("GET", "default", "ok") - beforeDefault

	if routeDelta != 1 {
		t.Errorf("expected 1 metric with path=/api/v0/clusters, got %.0f", routeDelta)
	}
	if defaultDelta != 1 {
		t.Errorf("expected 1 metric with path=default, got %.0f", defaultDelta)
	}
}

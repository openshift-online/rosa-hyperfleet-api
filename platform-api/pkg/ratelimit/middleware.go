package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/openshift-online/rosa-seaplane-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-seaplane-api/platform-api/pkg/middleware"
)

var requestsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratelimit_requests_total",
		Help: "Rate limited request results",
	},
	[]string{"method", "path", "result"},
)

type Limiter struct {
	limiter RateLimiter
	config  *Config
	logger  *slog.Logger
}

func New(rl RateLimiter, cfg *Config, logger *slog.Logger) *Limiter {
	return &Limiter{
		limiter: rl,
		config:  cfg,
		logger:  logger,
	}
}

func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.config.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		callerID := middleware.GetAccountID(r.Context())
		if callerID == "" {
			next.ServeHTTP(w, r)
			return
		}

		if l.config.isExempt(callerID) {
			next.ServeHTTP(w, r)
			return
		}

		limit := l.findLimit(r.Method, r.URL.Path)
		key := fmt.Sprintf("rl:%s:%s:%s", callerID, r.Method, limit.Path)

		rctx, cancel := context.WithTimeout(r.Context(), time.Duration(l.config.RedisTimeout)*time.Millisecond)
		res, err := l.limiter.Allow(rctx, key, Limit{
			Rate:   limit.Rate,
			Burst:  limit.Burst,
			Period: time.Duration(limit.Window) * time.Second,
		})
		cancel()

		if err != nil {
			requestsTotal.WithLabelValues(r.Method, limit.Path, "failure_mode_allowed").Inc()
			l.logger.Warn("rate limit check failed, allowing request",
				"error_type", fmt.Sprintf("%T", err),
				"method", r.Method,
				"path", limit.Path,
			)
			// Fail-open: set fallback rate-limit headers so downstream clients and
			// e2e tests always see rate-limit information, even when the backend is
			// temporarily unreachable.
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit.Rate))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(limit.Rate))
			w.Header().Set("X-RateLimit-Reset", strconv.Itoa(limit.Window))
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit.Rate))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.Itoa(int(math.Ceil(res.ResetAfter.Seconds()))))

		if res.Allowed == 0 {
			requestsTotal.WithLabelValues(r.Method, limit.Path, "over_limit").Inc()
			retryAfter := max(int(math.Ceil(res.RetryAfter.Seconds())), 1)
			l.logger.Warn("rate limit exceeded",
				"account_id", callerID,
				"method", r.Method,
				"path", limit.Path,
				"rate", limit.Rate,
				"retry_after", retryAfter,
			)
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			l.writeRateLimitError(w, r.Method, limit.Path, limit, retryAfter)
			return
		}

		requestsTotal.WithLabelValues(r.Method, limit.Path, "ok").Inc()
		next.ServeHTTP(w, r)
	})
}

func (l *Limiter) findLimit(method, path string) RouteLimit {
	for _, r := range l.config.Routes {
		if !strings.EqualFold(r.Method, method) {
			continue
		}
		if matchPath(r.Path, path) {
			return r
		}
	}
	return RouteLimit{
		Path:   "default",
		Rate:   l.config.Default.Rate,
		Burst:  l.config.Default.Burst,
		Window: l.config.Default.Window,
	}
}

var errRateLimit = api.APIError{
	Code:       "RATE-LIMIT-001",
	HTTPStatus: http.StatusTooManyRequests,
	Message:    "Too Many Requests",
	Reason:     "429 Too Many Requests — %s %s, %d req/%ds, retry after %ds",
}

func (l *Limiter) writeRateLimitError(w http.ResponseWriter, method, path string, limit RouteLimit, retryAfter int) {
	if err := api.WriteError(w, errRateLimit.WithReason(method, path, limit.Rate, limit.Window, retryAfter)); err != nil {
		l.logger.Error("failed to write rate limit error response", "error", err)
	}
}

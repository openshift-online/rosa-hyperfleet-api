# Security Audit — rosa-regional-platform-api

**Audit Date:** 2026-05-15  
**Auditor:** security-audit-agent  
**Severity Labels:** CRITICAL / HIGH / MEDIUM / LOW

---

## Finding 1 — CRITICAL: Authentication Based Entirely on Forgeable HTTP Headers

**File:** `pkg/middleware/identity.go`

```go
const (
    HeaderAccountID = "X-Amz-Account-Id"
    HeaderCallerARN = "X-Amz-Caller-Arn"
    HeaderUserID    = "X-Amz-User-Id"
    HeaderSourceIP  = "X-Amz-Source-Ip"
    HeaderRequestID = "X-Amz-Request-Id"
)

func Identity(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if accountID := r.Header.Get(HeaderAccountID); accountID != "" {
            ctx = context.WithValue(ctx, ContextKeyAccountID, accountID)
        }
        // ...
    })
}
```

**Risk:**  
The entire authentication model trusts HTTP headers (`X-Amz-Account-Id`, `X-Amz-Caller-Arn`) without any cryptographic verification that these headers were set by API Gateway. These headers are the **only** source of identity for the authorization pipeline: the `Authorization`, `AdminCheck`, `AccountCheck`, and `Authz` middleware all derive the identity from values in these headers.

If the API server is reachable by any path that bypasses API Gateway (e.g., direct pod IP, internal service name, misconfigured Kubernetes NetworkPolicy, VPC peering, or load balancer), any caller can forge any identity, including privileged accounts.

**Attack Vectors:**

1. **Direct pod access:** From within the EKS cluster (another pod, compromised node), call the API directly at `http://rosa-regional-platform-api:8000` with `X-Amz-Account-Id: 000000000000` (a hardcoded privileged account in test data) and `X-Amz-Caller-Arn: arn:aws:iam::000000000000:root` to gain full privileged access.
2. **Network policy gap:** If Kubernetes NetworkPolicy is not configured (or uses default-allow), any pod in the cluster can forge headers and call the API.
3. **API Gateway bypass:** If the ALB target group binding (`targetgroupbinding.yaml`) is misconfigured to also expose the pod directly, the API Gateway IAM auth layer is bypassed.
4. **Internal AWS service access:** VPC peering, Transit Gateway, or AWS PrivateLink configurations that allow other accounts/services to reach the pod's VPC could be used to forge headers.

**What to Mitigate:**

- Implement a **request signing validation** layer. API Gateway signs forwarded requests with a known secret. Validate that all requests carry this signature before trusting the `X-Amz-*` headers.
- Alternatively, configure **Kubernetes NetworkPolicy** to restrict ingress to the API pod to only the API Gateway VPC endpoint's source IP range.
- Add a shared secret or HMAC signature in a custom header that API Gateway injects (via a request transformer), which the Go service validates before processing any `X-Amz-*` header.
- Document and enforce that the API is **never** accessible without going through API Gateway (and validate this via automated network connectivity tests).

---

## Finding 2 — HIGH: Wildcard CORS Allows Cross-Origin Requests from Any Domain

**File:** `pkg/server/server.go:174-179`

```go
apiHandler := handlers.CORS(
    handlers.AllowedOrigins([]string{"*"}),
    handlers.AllowedMethods([]string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut}),
    handlers.AllowedHeaders([]string{"Content-Type", "Authorization"}),
)(apiRouter)
```

**Risk:**  
`AllowedOrigins([]string{"*"})` permits cross-origin requests from any domain. Because authentication relies on AWS SigV4-signed requests (injected by API Gateway), this CORS policy cannot be directly exploited for CSRF in typical browser flows. However, it creates several risks:

**Attack Vectors:**

1. **Information leakage:** Error responses, API versions (`/api/v0/info`), and health endpoints are accessible from any origin in a browser context without preflight restrictions, potentially leaking environment information.
2. **If custom auth headers are added:** If the API is ever extended with cookie-based auth or `Authorization` header-based auth (which is already listed in `AllowedHeaders`), the wildcard CORS policy immediately enables CSRF attacks from any origin.
3. **Reflected XSS pivot:** If any endpoint returns user-controlled content without escaping, the CORS policy enables cross-origin exploitation.
4. **Future regression:** As the API evolves, developers may not realize that wildcard CORS has been set, leading to accidental vulnerabilities.

**What to Mitigate:**  
Restrict `AllowedOrigins` to the specific frontend domains that need access (e.g., the ROSA console domain). If the API is purely machine-to-machine (not accessed from browsers), remove CORS headers entirely.

---

## Finding 3 — HIGH: Authorization Can Be Silently Disabled by Configuration

**File:** `pkg/server/server.go:87-102`

```go
if cfg.Authz != nil && cfg.Authz.Enabled {
    // Full Cedar/AVP authz setup
}
// else: only RequireAllowedAccount is applied (account allowlist only)
```

**Risk:**  
When `cfg.Authz` is `nil` or `cfg.Authz.Enabled` is `false`, the server falls back to the legacy `RequireAllowedAccount` middleware, which only checks if the `X-Amz-Account-Id` is in a static allowlist. This means:

- All accounts in the allowlist have full access to all operations, regardless of which IAM principal they use.
- There is no per-resource authorization, no admin requirement, and no Cedar policy evaluation.
- Any misconfiguration in the deployment (missing `AUTHZ_ENABLED=true` env var, missing DynamoDB config) silently degrades security.

**Attack Vector:**  
A deployment error (e.g., wrong ConfigMap, missing environment variable, failed DynamoDB connection that returns nil config) causes the server to start in the degraded `allowlist-only` mode. Any allowed account can perform all operations (create, delete, modify clusters, nodepools, etc.) without admin privileges.

**What to Mitigate:**

- **Fail-closed:** If authz configuration is expected but missing or invalid, the server should refuse to start rather than fall back to weaker controls.
- Add a startup assertion: if `cfg.Authz` is nil and the environment requires authz, log a fatal error and exit.
- Add a runtime health check that exposes the current auth mode, so monitoring can alert on degraded auth state.

---

## Finding 4 — HIGH: Privileged Account Check Error Silently Swallowed, Request Continues

**File:** `pkg/middleware/privileged.go:40-50`

```go
func (p *Privileged) CheckPrivileged(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // ...
        isPrivileged, err := p.authorizer.IsPrivileged(ctx, accountID)
        if err != nil {
            p.logger.Error("failed to check privileged status", "error", err, "account_id", accountID)
            // Continue without privileged status on error
            next.ServeHTTP(w, r)  // <-- request continues with no privileged flag set
            return
        }
        // ...
    })
}
```

**Risk:**  
When `IsPrivileged` fails (e.g., DynamoDB unavailable, network error), the middleware logs an error and calls `next.ServeHTTP`, allowing the request to continue. The `ContextKeyPrivileged` value is never set in the context, so `GetPrivileged(ctx)` returns `false` (the zero value for bool).

While the default `false` is correct for non-privileged accounts, this represents a **fail-open** pattern. The authorization checks downstream (`RequirePrivileged`, `Authorize`) may behave differently depending on whether the privileged flag was explicitly set to false vs. never set.

More critically: if `IsPrivileged` is called again downstream (as done in `RequirePrivileged`'s double-check), and DynamoDB is flapping, the behavior could be inconsistent across requests.

**Attack Vector:**  
An attacker who can cause transient DynamoDB errors (e.g., through a DDoS on the DynamoDB endpoint, or exploiting a misconfigured VPC endpoint throttle) could potentially cause repeated retries or race conditions in privilege evaluation.

**What to Mitigate:**  
Consider returning a 503 Service Unavailable response when the privilege check fails, rather than silently continuing. At minimum, document this as an explicit design decision with a risk acceptance sign-off.

---

## Finding 5 — MEDIUM: Prometheus Metrics Endpoint Exposed Without Authentication

**File:** `pkg/server/server.go:241-246`

```go
metricsRouter := mux.NewRouter()
metricsRouter.Handle("/metrics", promhttp.Handler()).Methods(http.MethodGet)
// ...
metricsServer: &http.Server{
    Addr: fmt.Sprintf("%s:%d", cfg.Server.MetricsBindAddress, cfg.Server.MetricsPort),
```

With `MetricsBindAddress: "0.0.0.0"` and `MetricsPort: 9090`.

**Risk:**  
The Prometheus metrics endpoint is bound to `0.0.0.0:9090` with no authentication. Prometheus metrics can expose:

- Request rates and patterns that reveal which API endpoints are being called (information useful for attackers)
- Error rates and types (useful for understanding what attacks are partially succeeding)
- Internal runtime metrics (goroutine counts, GC pauses, memory usage)
- Custom metrics that may include account IDs, cluster IDs, or other business-sensitive data if added in the future

**Attack Vector:**  
Any pod in the cluster can scrape `http://rosa-regional-platform-api:9090/metrics` and gain operational visibility into the API service. From within a compromised pod, this data can help an attacker understand traffic patterns and time attacks.

**What to Mitigate:**

- Bind the metrics server to `127.0.0.1` (localhost) only, and use Prometheus' in-cluster scraping (which uses the pod's localhost address, not a network service).
- Or restrict metrics access via Kubernetes NetworkPolicy to only the Prometheus scraper pod(s).
- Or add bearer token authentication to the metrics endpoint.

---

## Finding 6 — MEDIUM: Identity Headers Not Validated for Format or Bounds

**File:** `pkg/middleware/identity.go`

**Risk:**  
The `X-Amz-Account-Id`, `X-Amz-Caller-Arn`, and related headers are accepted without any format validation. AWS account IDs are always 12-digit numbers; ARNs follow a specific format (`arn:partition:service:region:account:resource`). Accepting arbitrary values:

1. **Log injection:** If these values are logged (and `authorization.go:36` logs `account_id`), a crafted value with newline characters could inject fake log entries, obfuscating an attack.
2. **Downstream injection:** If the ARN or account ID is used in downstream API calls (DynamoDB key, AVP policy evaluation), malformed values could cause unexpected behavior.
3. **Error message injection:** If account ID appears in error messages returned to users, it could be used for reflected injection attacks.

**What to Mitigate:**  
Validate that `X-Amz-Account-Id` is exactly a 12-digit string, and `X-Amz-Caller-Arn` matches the expected ARN format. Reject requests with malformed values with a 400 Bad Request response.

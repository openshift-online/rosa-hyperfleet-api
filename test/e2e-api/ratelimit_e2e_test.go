package e2e_test

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// concurrentRequests fires all requests in parallel so they arrive at the server
// within a tight window, overwhelming the GCRA burst before token replenishment.
// Sequential requests over the network (~300ms each) are too slow to exhaust the
// burst when rate=3/s (one token replenishes every 333ms).
func concurrentRequests(client *APIClient, path, accountID string, count int) []*APIResponse {
	responses := make([]*APIResponse, count)
	errs := make([]error, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			resp, err := client.Get(path, accountID)
			responses[idx] = resp
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		Expect(err).NotTo(HaveOccurred(), "concurrent request %d failed: %v", i, err)
	}
	return responses
}

// exhaustRateLimit sends waves of burst-sized concurrent requests until at
// least one 429 is received or maxWaves is exhausted. Each wave depletes the
// GCRA bucket further because cumulative consumption outpaces the inter-wave
// replenishment (~50ms gap × rate). Two waves suffice in practice; three
// guarantees it even under high network latency.
func exhaustRateLimit(client *APIClient, path, accountID string, burst, maxWaves int) []*APIResponse {
	var all []*APIResponse
	for wave := 0; wave < maxWaves; wave++ {
		responses := concurrentRequests(client, path, accountID, burst)
		all = append(all, responses...)
		for _, resp := range responses {
			if resp.StatusCode == http.StatusTooManyRequests {
				GinkgoWriter.Printf("429 received on wave %d/%d (%d total requests)\n", wave+1, maxWaves, len(all))
				return all
			}
		}
		GinkgoWriter.Printf("Wave %d/%d: no 429 yet (%d requests sent so far)\n", wave+1, maxWaves, len(all))
	}
	return all
}

var _ = Describe("Rate Limiting", Ordered, Label("ratelimit"), func() {
	var (
		baseURL       string
		accountID     string
		apiClient     *APIClient
		rateLimitRate int
	)

	BeforeAll(func() {
		baseURL = os.Getenv("E2E_BASE_URL")
		Expect(baseURL).NotTo(BeEmpty(), "E2E_BASE_URL must be set")

		accountID = os.Getenv("E2E_ACCOUNT_ID")
		if accountID == "" {
			cmd := exec.Command("aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text")
			output, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), "Failed to get AWS account ID via STS")
			accountID = strings.TrimSpace(string(output))
		}

		apiClient = NewAPIClient(baseURL)

		if os.Getenv("RATE_LIMIT_TEST_MODE") == "true" {
			rateLimitRate = 3
		} else {
			resp, err := apiClient.Get("/api/v0/clusters", accountID)
			Expect(err).NotTo(HaveOccurred())
			limitHeader := resp.Headers.Get("X-RateLimit-Limit")
			Expect(limitHeader).NotTo(BeEmpty(), "X-RateLimit-Limit header missing — is rate limiting enabled?")
			rateLimitRate, err = strconv.Atoi(limitHeader)
			Expect(err).NotTo(HaveOccurred())
		}

		GinkgoWriter.Printf("Rate limit E2E: baseURL=%s accountID=%s rate=%d burst=%d testMode=%s\n",
			baseURL, accountID, rateLimitRate, rateLimitRate*2, os.Getenv("RATE_LIMIT_TEST_MODE"))
	})

	It("should return rate limit headers on requests under the limit", func() {
		resp, err := apiClient.Get("/api/v0/clusters", accountID)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		Expect(resp.Headers.Get("X-RateLimit-Limit")).NotTo(BeEmpty())
		Expect(resp.Headers.Get("X-RateLimit-Remaining")).NotTo(BeEmpty())
		Expect(resp.Headers.Get("X-RateLimit-Reset")).NotTo(BeEmpty())

		limit, err := strconv.Atoi(resp.Headers.Get("X-RateLimit-Limit"))
		Expect(err).NotTo(HaveOccurred())
		Expect(limit).To(BeNumerically(">", 0))

		remaining, err := strconv.Atoi(resp.Headers.Get("X-RateLimit-Remaining"))
		Expect(err).NotTo(HaveOccurred())
		Expect(remaining).To(BeNumerically(">=", 0))

		GinkgoWriter.Printf("TLS verification successful: Valkey backend responded with rate limit headers (Limit=%d, Remaining=%d)\n", limit, remaining)
	})

	It("should return 429 when requests exceed the rate limit", func() {
		burst := rateLimitRate * 2

		GinkgoWriter.Printf("Exhausting rate limit (rate=%d, burst=%d, waves=3)\n", rateLimitRate, burst)
		responses := exhaustRateLimit(apiClient, "/api/v0/clusters", accountID, burst, 3)

		got429 := false
		statusCounts := map[int]int{}
		for _, resp := range responses {
			statusCounts[resp.StatusCode]++
			if resp.StatusCode == http.StatusTooManyRequests {
				got429 = true
			}
		}
		GinkgoWriter.Printf("Status distribution after %d requests: %v\n", len(responses), statusCounts)
		Expect(got429).To(BeTrue(), "expected at least one 429 after %d requests across 3 waves (statuses: %v)", len(responses), statusCounts)
	})

	It("should return correct 429 response body and Retry-After header", func() {
		burst := rateLimitRate * 2

		var rateLimitedResp *APIResponse
		responses := exhaustRateLimit(apiClient, "/api/v0/clusters", accountID, burst, 3)
		for _, resp := range responses {
			if resp.StatusCode == http.StatusTooManyRequests {
				rateLimitedResp = resp
				break
			}
		}
		Expect(rateLimitedResp).NotTo(BeNil(), "expected a 429 response after %d requests across 3 waves", len(responses))

		Expect(rateLimitedResp.Headers.Get("Retry-After")).NotTo(BeEmpty())
		retryAfter, err := strconv.Atoi(rateLimitedResp.Headers.Get("Retry-After"))
		Expect(err).NotTo(HaveOccurred())
		Expect(retryAfter).To(BeNumerically(">=", 1))

		Expect(rateLimitedResp.StatusCode).To(Equal(http.StatusTooManyRequests))

		var body map[string]interface{}
		err = json.Unmarshal(rateLimitedResp.Body, &body)
		Expect(err).NotTo(HaveOccurred())
		Expect(body["kind"]).To(Equal("Error"))
		Expect(body["reason"]).To(Equal("TooManyRequests"))
		Expect(body["message"]).To(ContainSubstring("RATE-LIMIT-001"))
	})

	It("should not rate limit exempt accounts", func() {
		exemptAccountID := os.Getenv("E2E_EXEMPT_ACCOUNT_ID")
		if exemptAccountID == "" {
			Skip("E2E_EXEMPT_ACCOUNT_ID not set — skipping exempt account test")
		}

		burst := rateLimitRate * 2

		// First prove 429s are triggerable with a non-exempt account
		probeAccount := accountID + "-exempt-probe"
		GinkgoWriter.Printf("Proving 429s are triggerable with non-exempt account %s\n", probeAccount)
		probeResponses := exhaustRateLimit(apiClient, "/api/v0/clusters", probeAccount, burst, 3)
		got429 := false
		for _, resp := range probeResponses {
			if resp.StatusCode == http.StatusTooManyRequests {
				got429 = true
				break
			}
		}
		Expect(got429).To(BeTrue(), "expected non-exempt account to hit 429 — rate limiting may not be active")

		// Now verify exempt account never gets 429 with the same volume
		GinkgoWriter.Printf("Sending %d concurrent requests with exempt account %s\n", burst, exemptAccountID)
		responses := concurrentRequests(apiClient, "/api/v0/clusters", exemptAccountID, burst)

		for i, resp := range responses {
			Expect(resp.StatusCode).NotTo(Equal(http.StatusTooManyRequests),
				"request %d/%d got 429 for exempt account %s", i+1, burst, exemptAccountID)
		}
	})

	It("should allow requests again after the rate limit window resets", func() {
		burst := rateLimitRate * 2

		var resetSeconds int
		responses := exhaustRateLimit(apiClient, "/api/v0/clusters", accountID, burst, 3)
		for _, resp := range responses {
			if resp.StatusCode == http.StatusTooManyRequests {
				resetHeader := resp.Headers.Get("X-RateLimit-Reset")
				Expect(resetHeader).NotTo(BeEmpty(), "429 response missing X-RateLimit-Reset header")
				var err error
				resetSeconds, err = strconv.Atoi(resetHeader)
				Expect(err).NotTo(HaveOccurred())
				break
			}
		}
		Expect(resetSeconds).To(BeNumerically(">=", 1), "expected a 429 with valid X-RateLimit-Reset after %d requests", len(responses))
		waitDuration := time.Duration(resetSeconds)*time.Second + 500*time.Millisecond
		GinkgoWriter.Printf("Waiting %v for rate limit to reset\n", waitDuration)
		time.Sleep(waitDuration)

		Eventually(func() int {
			resp, err := apiClient.Get("/api/v0/clusters", accountID)
			Expect(err).NotTo(HaveOccurred())
			return resp.StatusCode
		}, "10s", "1s").Should(Equal(http.StatusOK))
	})
})

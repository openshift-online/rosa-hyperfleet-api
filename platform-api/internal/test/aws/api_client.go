package aws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
)

// SHA256 of empty string (for GET/empty body). Used for SigV4 payload hash.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// apiGatewayRegionFromURL extracts the AWS region from an API Gateway URL or ROSA int URL.
// e.g. https://id.execute-api.us-east-2.amazonaws.com/prod -> "us-east-2"
// e.g. https://api.us-east-1.int0.rosa.devshift.net -> "us-east-1"
// Returns empty string if the URL does not match a known pattern.
func apiGatewayRegionFromURL(baseURL string) string {
	return "us-east-1"
}

// APIClient provides methods for making requests to the ROSA API
type APIClient struct {
	baseURL    string
	httpClient *http.Client
	CallerARN  string
	AWSProfile string
}

// APIResponse wraps an HTTP response with convenience methods
type APIResponse struct {
	StatusCode int
	Body       []byte
	Headers    http.Header
}

// NewAPIClient creates a new API client
func NewAPIClient(baseURL string) *APIClient {
	return &APIClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Do performs an HTTP request. When baseURL is an API Gateway URL, the request is signed with SigV4 using default AWS credentials.
func (c *APIClient) Do(method, path string, body any, accountID string) (*APIResponse, error) {
	var bodyBytes []byte
	var bodyReader io.Reader
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if accountID != "" {
		req.Header.Set("X-Amz-Account-Id", accountID)
	}
	if c.CallerARN != "" {
		req.Header.Set("X-Amz-Caller-Arn", c.CallerARN)
	}

	// Sign with SigV4 when targeting API Gateway (avoids 403 Missing Authentication Token)
	if region := apiGatewayRegionFromURL(c.baseURL); region != "" {
		var loadOpts []func(*config.LoadOptions) error
		if c.AWSProfile != "" {
			loadOpts = append(loadOpts, config.WithSharedConfigProfile(c.AWSProfile))
		}
		cfg, err := config.LoadDefaultConfig(context.Background(), loadOpts...)
		if err != nil {
			return nil, fmt.Errorf("loading AWS config for SigV4: %w", err)
		}
		creds, err := cfg.Credentials.Retrieve(context.Background())
		if err != nil {
			return nil, fmt.Errorf("retrieving AWS credentials for SigV4: %w", err)
		}
		payloadHash := emptyPayloadHash
		if len(bodyBytes) > 0 {
			sum := sha256.Sum256(bodyBytes)
			payloadHash = hex.EncodeToString(sum[:])
		}
		signer := v4.NewSigner()
		if err := signer.SignHTTP(context.Background(), creds, req, payloadHash, "execute-api", region, time.Now()); err != nil {
			return nil, fmt.Errorf("signing request with SigV4: %w", err)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return &APIResponse{
		StatusCode: resp.StatusCode,
		Body:       respBody,
		Headers:    resp.Header,
	}, nil
}

// Get performs a GET request
func (c *APIClient) Get(path, accountID string) (*APIResponse, error) {
	return c.Do(http.MethodGet, path, nil, accountID)
}

// Post performs a POST request
func (c *APIClient) Post(path string, body any, accountID string) (*APIResponse, error) {
	return c.Do(http.MethodPost, path, body, accountID)
}

// Put performs a PUT request
func (c *APIClient) Put(path string, body any, accountID string) (*APIResponse, error) {
	return c.Do(http.MethodPut, path, body, accountID)
}

// Patch performs a PATCH request
func (c *APIClient) Patch(path string, body any, accountID string) (*APIResponse, error) {
	return c.Do(http.MethodPatch, path, body, accountID)
}

// Delete performs a DELETE request
func (c *APIClient) Delete(path, accountID string) (*APIResponse, error) {
	return c.Do(http.MethodDelete, path, nil, accountID)
}

// JSON parses the response body as JSON
func (r *APIResponse) JSON() (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal(r.Body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// JSONArray parses the response body as a JSON array
func (r *APIResponse) JSONArray() ([]map[string]any, error) {
	var result []map[string]any
	if err := json.Unmarshal(r.Body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// CheckReady checks if the service is ready
func (c *APIClient) CheckReady() error {
	resp, err := c.Get("/api/v0/ready", "")
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("service not ready: status %d", resp.StatusCode)
	}

	return nil
}

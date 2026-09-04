package silence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AlertmanagerClient implements Client against Alertmanager HTTP API v2.
type AlertmanagerClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewAlertmanagerClient returns a client for the given Alertmanager base URL.
func NewAlertmanagerClient(baseURL string, httpClient *http.Client) *AlertmanagerClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &AlertmanagerClient{BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: httpClient}
}

func (c *AlertmanagerClient) List(ctx context.Context, identity ClusterIdentity) ([]GettableSilence, error) {
	values := url.Values{}
	values.Add("filter", fmt.Sprintf(`namespace=%q`, identity.Namespace))
	values.Add("filter", fmt.Sprintf(`name=%q`, identity.Name))
	endpoint := c.BaseURL + "/api/v2/silences?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build list request: %w", err)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list silences: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list silences: %s", readResponseStatus(resp))
	}
	var silences []GettableSilence
	if err := json.NewDecoder(resp.Body).Decode(&silences); err != nil {
		return nil, fmt.Errorf("decode silences: %w", err)
	}
	var managed []GettableSilence
	for _, s := range silences {
		if s.CreatedBy == CreatedBy && s.Status.State == "active" {
			managed = append(managed, s)
		}
	}
	return managed, nil
}

func (c *AlertmanagerClient) Create(ctx context.Context, silence PostableSilence) (string, error) {
	body, err := json.Marshal(silence)
	if err != nil {
		return "", fmt.Errorf("marshal silence: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v2/silences", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create silence: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("create silence: %s", readResponseStatus(resp))
	}
	var out struct {
		SilenceID string `json:"silenceID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode create response: %w", err)
	}
	if out.SilenceID == "" {
		return "", fmt.Errorf("create silence: empty silenceID in response")
	}
	return out.SilenceID, nil
}

func (c *AlertmanagerClient) Expire(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/api/v2/silence/"+url.PathEscape(id), nil)
	if err != nil {
		return fmt.Errorf("build expire request: %w", err)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("expire silence: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expire silence: %s", readResponseStatus(resp))
	}
	return nil
}

func readResponseStatus(resp *http.Response) string {
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); err != nil {
		return fmt.Sprintf("%s (read response body failed)", resp.Status)
	}
	return resp.Status
}

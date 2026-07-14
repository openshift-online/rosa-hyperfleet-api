package hyperfleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/openshift/rosa-hyperfleet-api/pkg/config"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
	"github.com/openshift/rosa-hyperfleet-api/pkg/types"
)

const (
	clustersPath = "/api/hyperfleet/v1/clusters"
)

// Client provides access to the Hyperfleet API
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewClient creates a new Hyperfleet client
func NewClient(cfg config.HyperfleetConfig, logger *slog.Logger) *Client {
	return &Client{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		logger: logger,
	}
}

// setAWSHeaders adds AWS identity headers to the HTTP request
func (c *Client) setAWSHeaders(req *http.Request, ctx context.Context) {
	if accountID := middleware.GetAccountID(ctx); accountID != "" {
		req.Header.Set("X-Amz-Account-Id", accountID)
	}
	if callerARN := middleware.GetCallerARN(ctx); callerARN != "" {
		req.Header.Set("X-Amz-Caller-Arn", callerARN)
	}
	if userID := middleware.GetUserID(ctx); userID != "" {
		req.Header.Set("X-Amz-User-Id", userID)
	}
}

// parseError parses an error response from Hyperfleet API
func (c *Client) parseError(statusCode int, body []byte) error {
	var hfErr Error
	if json.Unmarshal(body, &hfErr) == nil && (hfErr.Message != "" || hfErr.Reason != "" || hfErr.Detail != "") {
		if hfErr.Code == "" {
			hfErr.Code = strconv.Itoa(statusCode)
		}
		// Preserve HTTP status code if not already set by Hyperfleet
		if hfErr.Status == 0 {
			hfErr.Status = statusCode
		}
		return &hfErr
	}
	return &Error{
		Code:    strconv.Itoa(statusCode),
		Status:  statusCode,
		Message: string(body),
	}
}

// ListClusters lists clusters from Hyperfleet with pagination
func (c *Client) ListClusters(ctx context.Context, accountID string, limit, offset int, status string) ([]*types.Cluster, int, error) {
	// Convert offset/limit to page/pageSize
	page := 1
	if limit > 0 && offset > 0 {
		page = (offset / limit) + 1
	}
	pageSize := limit
	if pageSize <= 0 {
		pageSize = 50
	}

	u, err := url.Parse(c.baseURL + clustersPath)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to parse URL: %w", err)
	}

	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	q.Set("pageSize", strconv.Itoa(pageSize))
	if status != "" {
		q.Set("status", status)
	}
	u.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAWSHeaders(httpReq, ctx)

	c.logger.Debug("listing clusters from Hyperfleet", "account_id", accountID, "page", page, "pageSize", pageSize)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, 0, c.parseError(resp.StatusCode, respBody)
	}

	var hfList HFClusterList
	if err := json.Unmarshal(respBody, &hfList); err != nil {
		return nil, 0, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Transform Hyperfleet clusters to platform clusters
	clusters := make([]*types.Cluster, 0, len(hfList.Items))
	for i := range hfList.Items {
		clusters = append(clusters, hyperfleetToPlatformCluster(&hfList.Items[i]))
	}

	c.logger.Debug("clusters listed from Hyperfleet", "total", hfList.TotalCount, "returned", len(clusters))

	return clusters, hfList.TotalCount, nil
}

// CreateCluster creates a new cluster in Hyperfleet
func (c *Client) CreateCluster(ctx context.Context, accountID, userEmail string, req *types.ClusterCreateRequest) (*types.Cluster, error) {
	// Transform platform request to Hyperfleet request
	hfReq := platformToHyperfleetCreate(req, userEmail)

	body, err := json.Marshal(hfReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+clustersPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	c.setAWSHeaders(httpReq, ctx)

	c.logger.Debug("creating cluster in Hyperfleet", "account_id", accountID, "cluster_name", req.Name)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, c.parseError(resp.StatusCode, respBody)
	}

	var hfCluster HFCluster
	if err := json.Unmarshal(respBody, &hfCluster); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	c.logger.Debug("cluster created in Hyperfleet", "id", hfCluster.ID, "name", hfCluster.Name)

	return hyperfleetToPlatformCluster(&hfCluster), nil
}

// GetCluster retrieves a cluster by ID from Hyperfleet
func (c *Client) GetCluster(ctx context.Context, accountID, clusterID string) (*types.Cluster, error) {
	path := fmt.Sprintf("%s/%s", clustersPath, url.PathEscape(clusterID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAWSHeaders(httpReq, ctx)

	c.logger.Debug("getting cluster from Hyperfleet", "account_id", accountID, "cluster_id", clusterID)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp.StatusCode, respBody)
	}

	var hfCluster HFCluster
	if err := json.Unmarshal(respBody, &hfCluster); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	c.logger.Debug("cluster retrieved from Hyperfleet", "id", hfCluster.ID, "name", hfCluster.Name)

	return hyperfleetToPlatformCluster(&hfCluster), nil
}

// UpdateCluster updates a cluster in Hyperfleet
func (c *Client) UpdateCluster(ctx context.Context, accountID, clusterID string, req *types.ClusterUpdateRequest) (*types.Cluster, error) {
	// Transform platform request to Hyperfleet request
	hfReq := platformToHyperfleetUpdate(req)

	body, err := json.Marshal(hfReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	path := fmt.Sprintf("%s/%s", clustersPath, url.PathEscape(clusterID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	c.setAWSHeaders(httpReq, ctx)

	c.logger.Debug("updating cluster in Hyperfleet",
		"account_id", accountID,
		"cluster_id", clusterID,
		"request_size", len(body))

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("hyperfleet returned error on cluster update",
			"status_code", resp.StatusCode,
			"response_size", len(respBody),
			"cluster_id", clusterID,
			"account_id", accountID)
		return nil, c.parseError(resp.StatusCode, respBody)
	}

	var hfCluster HFCluster
	if err := json.Unmarshal(respBody, &hfCluster); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	c.logger.Debug("cluster updated in Hyperfleet", "id", hfCluster.ID, "name", hfCluster.Name)

	return hyperfleetToPlatformCluster(&hfCluster), nil
}

// DeleteCluster deletes a cluster in Hyperfleet
func (c *Client) DeleteCluster(ctx context.Context, accountID, clusterID string, force bool) error {
	u, err := url.Parse(c.baseURL + fmt.Sprintf("%s/%s", clustersPath, url.PathEscape(clusterID)))
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}

	if force {
		q := u.Query()
		q.Set("force", "true")
		u.RawQuery = q.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setAWSHeaders(httpReq, ctx)

	c.logger.Debug("deleting cluster in Hyperfleet", "account_id", accountID, "cluster_id", clusterID, "force", force)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return c.parseError(resp.StatusCode, respBody)
	}

	c.logger.Debug("cluster deletion initiated in Hyperfleet", "cluster_id", clusterID)

	return nil
}

// GetClusterStatus retrieves cluster status from Hyperfleet, including adapter statuses
func (c *Client) GetClusterStatus(ctx context.Context, accountID, clusterID string) (*types.ClusterStatusResponse, error) {
	// Get main cluster info
	cluster, err := c.GetCluster(ctx, accountID, clusterID)
	if err != nil {
		return nil, err
	}

	// Get adapter statuses
	statusPath := fmt.Sprintf("%s/%s/statuses", clustersPath, url.PathEscape(clusterID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+statusPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAWSHeaders(httpReq, ctx)

	c.logger.Debug("getting cluster statuses from Hyperfleet", "account_id", accountID, "cluster_id", clusterID)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Adapter statuses might not exist yet, so 404 is acceptable
	var adapterStatuses []HFAdapterStatus
	if resp.StatusCode == http.StatusOK {
		var hfStatusList HFAdapterStatusList
		if err := json.Unmarshal(respBody, &hfStatusList); err != nil {
			c.logger.Warn("failed to unmarshal adapter statuses", "error", err)
		} else {
			adapterStatuses = hfStatusList.Items
		}
	} else if resp.StatusCode != http.StatusNotFound {
		c.logger.Warn("unexpected status code when fetching adapter statuses", "status_code", resp.StatusCode)
	}

	// Transform adapter statuses to controller statuses
	controllerStatuses := adapterStatusesToControllerStatuses(adapterStatuses)

	c.logger.Debug("cluster status retrieved from Hyperfleet", "cluster_id", clusterID, "controller_count", len(controllerStatuses))

	return &types.ClusterStatusResponse{
		ClusterID:          cluster.ID,
		Status:             cluster.Status,
		ControllerStatuses: controllerStatuses,
	}, nil
}

// Transformation functions

// platformToHyperfleetCreate transforms platform ClusterCreateRequest to Hyperfleet format
func platformToHyperfleetCreate(req *types.ClusterCreateRequest, userEmail string) *HFClusterCreateRequest {
	labels := make(map[string]string)
	if req.TargetProjectID != "" {
		labels["target_project_id"] = req.TargetProjectID
	}

	return &HFClusterCreateRequest{
		Kind:      ClusterKind,
		Name:      req.Name,
		Labels:    labels,
		Spec:      req.Spec,
		CreatedBy: userEmail,
	}
}

// platformToHyperfleetUpdate transforms platform ClusterUpdateRequest to Hyperfleet format
func platformToHyperfleetUpdate(req *types.ClusterUpdateRequest) *HFClusterUpdateRequest {
	return &HFClusterUpdateRequest{
		Spec: req.Spec,
	}
}

// hyperfleetToPlatformCluster transforms Hyperfleet cluster to platform format
func hyperfleetToPlatformCluster(hfCluster *HFCluster) *types.Cluster {
	cluster := &types.Cluster{
		ID:              hfCluster.ID,
		Name:            hfCluster.Name,
		TargetProjectID: hfCluster.Labels["target_project_id"],
		CreatedBy:       hfCluster.CreatedBy,
		Generation:      hfCluster.Generation,
		ResourceVersion: fmt.Sprintf("%d", hfCluster.Generation),
		Spec:            hfCluster.Spec,
		CreatedAt:       hfCluster.CreatedAt,
		UpdatedAt:       hfCluster.UpdatedAt,
	}

	// Transform status if present
	if hfCluster.Status != nil {
		cluster.Status = &types.ClusterStatusInfo{
			ObservedGeneration: hfCluster.Status.ObservedGeneration,
			Phase:              hfCluster.Status.Phase,
			Message:            hfCluster.Status.Message,
			Reason:             hfCluster.Status.Reason,
			LastUpdateTime:     hfCluster.Status.LastUpdateTime,
		}

		// Transform conditions
		if len(hfCluster.Status.Conditions) > 0 {
			cluster.Status.Conditions = make([]types.Condition, 0, len(hfCluster.Status.Conditions))
			for _, hfCond := range hfCluster.Status.Conditions {
				cluster.Status.Conditions = append(cluster.Status.Conditions, types.Condition{
					Type:               hfCond.Type,
					Status:             hfCond.Status,
					LastTransitionTime: hfCond.LastTransitionTime,
					Reason:             hfCond.Reason,
					Message:            hfCond.Message,
				})
			}
		}
	}

	return cluster
}

// adapterStatusesToControllerStatuses transforms Hyperfleet adapter statuses to platform controller statuses
func adapterStatusesToControllerStatuses(adapterStatuses []HFAdapterStatus) []*types.ClusterControllerStatus {
	controllerStatuses := make([]*types.ClusterControllerStatus, 0, len(adapterStatuses))

	for _, adapterStatus := range adapterStatuses {
		controllerStatus := &types.ClusterControllerStatus{
			ClusterID:          adapterStatus.ClusterID,
			ControllerName:     adapterStatus.AdapterName,
			ObservedGeneration: adapterStatus.ObservedGeneration,
			Metadata:           adapterStatus.Metadata,
			Data:               adapterStatus.Data,
			LastUpdated:        adapterStatus.LastUpdated,
		}

		// Transform conditions
		if len(adapterStatus.Conditions) > 0 {
			controllerStatus.Conditions = make([]types.Condition, 0, len(adapterStatus.Conditions))
			for _, hfCond := range adapterStatus.Conditions {
				controllerStatus.Conditions = append(controllerStatus.Conditions, types.Condition{
					Type:               hfCond.Type,
					Status:             hfCond.Status,
					LastTransitionTime: hfCond.LastTransitionTime,
					Reason:             hfCond.Reason,
					Message:            hfCond.Message,
				})
			}
		}

		controllerStatuses = append(controllerStatuses, controllerStatus)
	}

	return controllerStatuses
}

package hyperfleet

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/openshift/rosa-hyperfleet-api/pkg/config"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
	"github.com/openshift/rosa-hyperfleet-api/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListClusters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/hyperfleet/v1/clusters", r.URL.Path)

		// Verify AWS headers
		assert.Equal(t, "123456789012", r.Header.Get("X-Amz-Account-Id"))
		assert.Equal(t, "arn:aws:iam::123456789012:user/test", r.Header.Get("X-Amz-Caller-Arn"))
		assert.Equal(t, "test@example.com", r.Header.Get("X-Amz-User-Id"))

		// Verify query parameters
		assert.Equal(t, "1", r.URL.Query().Get("page"))
		assert.Equal(t, "50", r.URL.Query().Get("pageSize"))

		// Return mock response
		resp := HFClusterList{
			Items: []HFCluster{
				{
					ID:         "cluster-1",
					Name:       "test-cluster",
					Labels:     map[string]string{"target_project_id": "project-1"},
					Spec:       map[string]interface{}{"provider": "aws"},
					Generation: 1,
					CreatedBy:  "test@example.com",
					CreatedAt:  time.Now(),
					UpdatedAt:  time.Now(),
				},
			},
			TotalCount: 1,
			Page:       1,
			PageSize:   50,
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)

	// Create context with AWS identity
	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, "123456789012")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::123456789012:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "test@example.com")

	clusters, total, err := client.ListClusters(ctx, "123456789012", 50, 0, "")

	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Len(t, clusters, 1)
	assert.Equal(t, "cluster-1", clusters[0].ID)
	assert.Equal(t, "test-cluster", clusters[0].Name)
	assert.Equal(t, "project-1", clusters[0].TargetProjectID)
}

func TestCreateCluster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/hyperfleet/v1/clusters", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Verify AWS headers
		assert.Equal(t, "123456789012", r.Header.Get("X-Amz-Account-Id"))

		// Verify request body
		var req HFClusterCreateRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, "Cluster", req.Kind)
		assert.Equal(t, "test-cluster", req.Name)
		assert.Equal(t, "project-1", req.Labels["target_project_id"])
		assert.Equal(t, "aws", req.Spec["provider"])
		assert.Equal(t, "test@example.com", req.CreatedBy)

		// Return mock response
		resp := HFCluster{
			ID:         "cluster-1",
			Name:       req.Name,
			Labels:     req.Labels,
			Spec:       req.Spec,
			Generation: 1,
			CreatedBy:  req.CreatedBy,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)

	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, "123456789012")

	req := &types.ClusterCreateRequest{
		Name:            "test-cluster",
		TargetProjectID: "project-1",
		Spec:            map[string]interface{}{"provider": "aws"},
	}

	cluster, err := client.CreateCluster(ctx, "123456789012", "test@example.com", req)

	require.NoError(t, err)
	assert.Equal(t, "cluster-1", cluster.ID)
	assert.Equal(t, "test-cluster", cluster.Name)
	assert.Equal(t, "project-1", cluster.TargetProjectID)
	assert.Equal(t, "test@example.com", cluster.CreatedBy)
}

func TestGetCluster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/hyperfleet/v1/clusters/cluster-1", r.URL.Path)

		// Verify AWS headers
		assert.Equal(t, "123456789012", r.Header.Get("X-Amz-Account-Id"))

		// Return mock response
		resp := HFCluster{
			ID:         "cluster-1",
			Name:       "test-cluster",
			Labels:     map[string]string{"target_project_id": "project-1"},
			Spec:       map[string]interface{}{"provider": "aws"},
			Generation: 1,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
			Status: &HFClusterStatus{
				ObservedGeneration: 1,
				Phase:              "Ready",
				Message:            "Cluster is ready",
				LastUpdateTime:     time.Now(),
				Conditions: []HFCondition{
					{
						Type:               "Ready",
						Status:             "True",
						LastTransitionTime: time.Now(),
						Reason:             "ClusterReady",
						Message:            "Cluster is ready",
					},
				},
			},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)

	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, "123456789012")

	cluster, err := client.GetCluster(ctx, "123456789012", "cluster-1")

	require.NoError(t, err)
	assert.Equal(t, "cluster-1", cluster.ID)
	assert.Equal(t, "test-cluster", cluster.Name)
	assert.NotNil(t, cluster.Status)
	assert.Equal(t, "Ready", cluster.Status.Phase)
	assert.Len(t, cluster.Status.Conditions, 1)
}

func TestGetCluster_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		resp := Error{
			Code:    "404",
			Message: "Cluster not found",
			Reason:  "The requested cluster does not exist",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)

	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, "123456789012")

	cluster, err := client.GetCluster(ctx, "123456789012", "nonexistent")

	require.Error(t, err)
	assert.Nil(t, cluster)
	assert.True(t, IsNotFound(err))
}

func TestUpdateCluster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/api/hyperfleet/v1/clusters/cluster-1", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Verify request body
		var req HFClusterUpdateRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, "gcp", req.Spec["provider"])

		// Return mock response
		resp := HFCluster{
			ID:         "cluster-1",
			Name:       "test-cluster",
			Spec:       req.Spec,
			Generation: 2,
			CreatedAt:  time.Now().Add(-1 * time.Hour),
			UpdatedAt:  time.Now(),
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)

	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, "123456789012")

	req := &types.ClusterUpdateRequest{
		Spec: map[string]interface{}{"provider": "gcp"},
	}

	cluster, err := client.UpdateCluster(ctx, "123456789012", "cluster-1", req)

	require.NoError(t, err)
	assert.Equal(t, "cluster-1", cluster.ID)
	assert.Equal(t, int64(2), cluster.Generation)
	assert.Equal(t, "gcp", cluster.Spec["provider"])
}

func TestDeleteCluster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/hyperfleet/v1/clusters/cluster-1", r.URL.Path)

		// Verify force parameter
		assert.Equal(t, "true", r.URL.Query().Get("force"))

		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)

	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, "123456789012")

	err := client.DeleteCluster(ctx, "123456789012", "cluster-1", true)

	require.NoError(t, err)
}

func TestGetClusterStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/hyperfleet/v1/clusters/cluster-1":
			// Return cluster info
			resp := HFCluster{
				ID:         "cluster-1",
				Name:       "test-cluster",
				Generation: 1,
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
				Status: &HFClusterStatus{
					ObservedGeneration: 1,
					Phase:              "Ready",
					LastUpdateTime:     time.Now(),
				},
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/hyperfleet/v1/clusters/cluster-1/statuses":
			// Return adapter statuses
			resp := HFAdapterStatusList{
				Items: []HFAdapterStatus{
					{
						ClusterID:          "cluster-1",
						AdapterName:        "network-adapter",
						ObservedGeneration: 1,
						LastUpdated:        time.Now(),
						Conditions: []HFCondition{
							{
								Type:               "Ready",
								Status:             "True",
								LastTransitionTime: time.Now(),
							},
						},
					},
					{
						ClusterID:          "cluster-1",
						AdapterName:        "compute-adapter",
						ObservedGeneration: 1,
						LastUpdated:        time.Now(),
						Conditions: []HFCondition{
							{
								Type:               "Ready",
								Status:             "True",
								LastTransitionTime: time.Now(),
							},
						},
					},
				},
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)

	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, "123456789012")

	status, err := client.GetClusterStatus(ctx, "123456789012", "cluster-1")

	require.NoError(t, err)
	assert.Equal(t, "cluster-1", status.ClusterID)
	assert.NotNil(t, status.Status)
	assert.Equal(t, "Ready", status.Status.Phase)
	assert.Len(t, status.ControllerStatuses, 2)
	assert.Equal(t, "network-adapter", status.ControllerStatuses[0].ControllerName)
	assert.Equal(t, "compute-adapter", status.ControllerStatuses[1].ControllerName)
}

func TestPaginationConversion(t *testing.T) {
	tests := []struct {
		name         string
		limit        int
		offset       int
		expectedPage int
		expectedSize int
	}{
		{
			name:         "first page",
			limit:        50,
			offset:       0,
			expectedPage: 1,
			expectedSize: 50,
		},
		{
			name:         "second page",
			limit:        50,
			offset:       50,
			expectedPage: 2,
			expectedSize: 50,
		},
		{
			name:         "third page",
			limit:        25,
			offset:       50,
			expectedPage: 3,
			expectedSize: 25,
		},
		{
			name:         "default limit",
			limit:        0,
			offset:       0,
			expectedPage: 1,
			expectedSize: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, strconv.Itoa(tt.expectedPage), r.URL.Query().Get("page"))
				assert.Equal(t, strconv.Itoa(tt.expectedSize), r.URL.Query().Get("pageSize"))

				resp := HFClusterList{
					Items:      []HFCluster{},
					TotalCount: 0,
					Page:       tt.expectedPage,
					PageSize:   tt.expectedSize,
				}
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			client := NewClient(config.HyperfleetConfig{
				BaseURL: server.URL,
				Timeout: 30 * time.Second,
			}, logger)

			ctx := context.Background()
			_, _, err := client.ListClusters(ctx, "123456789012", tt.limit, tt.offset, "")
			require.NoError(t, err)
		})
	}
}

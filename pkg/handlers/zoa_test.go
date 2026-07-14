package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
	"github.com/openshift/rosa-hyperfleet-api/pkg/zoa"
	workv1 "open-cluster-management.io/api/work/v1"
)

type mockExecutionStore struct {
	createFunc             func(ctx context.Context, exec *zoa.Execution) error
	getFunc                func(ctx context.Context, executionID string) (*zoa.Execution, error)
	listFunc               func(ctx context.Context, accountID string, limit int, filter *zoa.ListFilter) ([]*zoa.Execution, error)
	updateStatusFunc       func(ctx context.Context, executionID string, status zoa.ExecutionStatus, completedAt string, duration int) error
	updateManifestWorkFunc func(ctx context.Context, executionID, mwName string) error
	listPendingFunc        func(ctx context.Context) ([]*zoa.Execution, error)
}

func (m *mockExecutionStore) Create(ctx context.Context, exec *zoa.Execution) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, exec)
	}
	return nil
}

func (m *mockExecutionStore) Get(ctx context.Context, executionID string) (*zoa.Execution, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, executionID)
	}
	return nil, nil
}

func (m *mockExecutionStore) List(ctx context.Context, accountID string, limit int, filter *zoa.ListFilter) ([]*zoa.Execution, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, accountID, limit, filter)
	}
	return nil, nil
}

func (m *mockExecutionStore) UpdateStatus(ctx context.Context, executionID string, status zoa.ExecutionStatus, completedAt string, duration int) error {
	if m.updateStatusFunc != nil {
		return m.updateStatusFunc(ctx, executionID, status, completedAt, duration)
	}
	return nil
}

func (m *mockExecutionStore) UpdateCompletion(ctx context.Context, executionID string, status zoa.ExecutionStatus, completedAt string, duration int, runnerSeconds int, uploadSeconds int, outputStatus zoa.OutputStatus) error {
	return nil
}

func (m *mockExecutionStore) UpdateManifestWorkName(ctx context.Context, executionID, mwName string) error {
	if m.updateManifestWorkFunc != nil {
		return m.updateManifestWorkFunc(ctx, executionID, mwName)
	}
	return nil
}

func (m *mockExecutionStore) ListPending(ctx context.Context) ([]*zoa.Execution, error) {
	if m.listPendingFunc != nil {
		return m.listPendingFunc(ctx)
	}
	return nil, nil
}

type zoaMockMaestroClient struct {
	createManifestWorkFunc func(ctx context.Context, clusterName string, mw *workv1.ManifestWork) (*workv1.ManifestWork, error)
}

func (m *zoaMockMaestroClient) CreateConsumer(ctx context.Context, req *maestro.ConsumerCreateRequest) (*maestro.Consumer, error) {
	return nil, nil
}

func (m *zoaMockMaestroClient) ListConsumers(ctx context.Context, page, size int) (*maestro.ConsumerList, error) {
	return nil, nil
}

func (m *zoaMockMaestroClient) GetConsumer(ctx context.Context, id string) (*maestro.Consumer, error) {
	return nil, nil
}

func (m *zoaMockMaestroClient) ListResourceBundles(ctx context.Context, page, size int, search, orderBy, fields string) (*maestro.ResourceBundleList, error) {
	return nil, nil
}

func (m *zoaMockMaestroClient) GetResourceBundle(ctx context.Context, id string) (*maestro.ResourceBundle, error) {
	return nil, nil
}

func (m *zoaMockMaestroClient) GetManifestWork(ctx context.Context, clusterName string, name string) (*workv1.ManifestWork, error) {
	return nil, nil
}

func (m *zoaMockMaestroClient) DeleteResourceBundle(ctx context.Context, id string) error {
	return nil
}

func (m *zoaMockMaestroClient) DeleteManifestWork(ctx context.Context, clusterName string, name string) error {
	return nil
}

func (m *zoaMockMaestroClient) CreateManifestWork(ctx context.Context, clusterName string, mw *workv1.ManifestWork) (*workv1.ManifestWork, error) {
	if m.createManifestWorkFunc != nil {
		return m.createManifestWorkFunc(ctx, clusterName, mw)
	}
	result := mw.DeepCopy()
	result.Name = "zoa-test-work"
	return result, nil
}

type mockS3Client struct{}

func (m *mockS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return &s3.GetObjectOutput{
		Body: io.NopCloser(strings.NewReader(`{"summary": "test output"}`)),
	}, nil
}

func testZoaLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testJobConfig() *zoa.JobConfig {
	return &zoa.JobConfig{
		Image:         "quay.io/test/zoa-tools:latest",
		CPURequest:    "100m",
		MemoryRequest: "128Mi",
		CPULimit:      "500m",
		MemoryLimit:   "512Mi",
		TTLSeconds:    3600,
		EntrypointScript: `#!/bin/bash
set -uo pipefail
/zoa/run.sh
`,
	}
}

func testTemplateRegistry(t *testing.T) *zoa.TemplateRegistry {
	t.Helper()
	dir := t.TempDir()
	templateContent := `name: get_nodes
scope: kube-api
type: read
description: List all nodes in the target cluster
params:
  - name: node_selector
    required: false
    default: ""
rbac:
  cluster_scoped: true
  rules:
    - apiGroups: [""]
      resources: ["nodes"]
      verbs: ["get", "list"]
script: |
  kubectl get nodes -o json > /artifacts/output.json
`
	err := os.WriteFile(dir+"/get_nodes.yaml", []byte(templateContent), 0644)
	require.NoError(t, err)

	registry := zoa.NewTemplateRegistry(testZoaLogger())
	err = registry.LoadFromDir(dir)
	require.NoError(t, err)
	return registry
}

func newTestZoaHandler(t *testing.T, store zoa.ExecutionStore, maestroClient *zoaMockMaestroClient) *ZoaHandler {
	t.Helper()
	return NewZoaHandler(store, testTemplateRegistry(t), maestroClient, &mockS3Client{}, ZoaConfig{
		BucketName: "test-bucket",
		JobConfig:  testJobConfig(),
	}, testZoaLogger())
}

func TestZoaHandler_Create_Success(t *testing.T) {
	store := &mockExecutionStore{}
	mc := &zoaMockMaestroClient{}
	handler := newTestZoaHandler(t, store, mc)

	body := `{"target_cluster": "mc01", "jira": "ROSAENG-1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/get_nodes/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "get_nodes"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusAccepted, rr.Code)

	var resp zoa.Execution
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "get_nodes", resp.Action)
	assert.Equal(t, "mc01", resp.TargetCluster)
	assert.Equal(t, "ROSAENG-1234", resp.Jira)
	assert.Equal(t, zoa.StatusPending, resp.Status)
	assert.Equal(t, "read", resp.Type)
	assert.Equal(t, "kube-api", resp.Scope)
	assert.Equal(t, "test", resp.Operator)
	assert.NotEmpty(t, resp.ExecutionID)
}

func TestZoaHandler_Create_MissingJira(t *testing.T) {
	store := &mockExecutionStore{}
	mc := &zoaMockMaestroClient{}
	handler := newTestZoaHandler(t, store, mc)

	body := `{"target_cluster": "mc01"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/get_nodes/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "get_nodes"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "missing-jira")
}

func TestZoaHandler_Create_UnknownAction(t *testing.T) {
	store := &mockExecutionStore{}
	mc := &zoaMockMaestroClient{}
	handler := newTestZoaHandler(t, store, mc)

	body := `{"target_cluster": "mc01"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/nonexistent/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "nonexistent"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestZoaHandler_Create_MissingTargetCluster(t *testing.T) {
	store := &mockExecutionStore{}
	mc := &zoaMockMaestroClient{}
	handler := newTestZoaHandler(t, store, mc)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/get_nodes/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "get_nodes"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestZoaHandler_Get_Found(t *testing.T) {
	store := &mockExecutionStore{
		getFunc: func(ctx context.Context, executionID string) (*zoa.Execution, error) {
			return &zoa.Execution{
				ExecutionID:   "exec-123",
				Action:        "get_nodes",
				Status:        zoa.StatusSucceeded,
				TargetCluster: "mc01",
				OutputPath:    "exec-123/output.json",
				OutputStatus:  zoa.OutputStatusUploaded,
			}, nil
		},
	}
	handler := newTestZoaHandler(t, store, &zoaMockMaestroClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/runs/exec-123?include=output", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "exec-123"})

	rr := httptest.NewRecorder()
	handler.Get(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp zoa.ExecutionResponse
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "exec-123", resp.Execution.ExecutionID)
	assert.NotNil(t, resp.Output)
}

func TestZoaHandler_Get_NotFound(t *testing.T) {
	store := &mockExecutionStore{
		getFunc: func(ctx context.Context, executionID string) (*zoa.Execution, error) {
			return nil, nil
		},
	}
	handler := newTestZoaHandler(t, store, &zoaMockMaestroClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/runs/nonexistent", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "nonexistent"})

	rr := httptest.NewRecorder()
	handler.Get(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestZoaHandler_List(t *testing.T) {
	store := &mockExecutionStore{
		listFunc: func(ctx context.Context, accountID string, limit int, filter *zoa.ListFilter) ([]*zoa.Execution, error) {
			return []*zoa.Execution{
				{ExecutionID: "exec-1", Action: "get_nodes", Status: zoa.StatusSucceeded},
				{ExecutionID: "exec-2", Action: "get_nodes", Status: zoa.StatusPending},
			}, nil
		},
	}
	handler := newTestZoaHandler(t, store, &zoaMockMaestroClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/runs", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))

	rr := httptest.NewRecorder()
	handler.List(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp zoa.ExecutionList
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Total)
	assert.Len(t, resp.Items, 2)
}

func TestZoaHandler_Describe(t *testing.T) {
	handler := newTestZoaHandler(t, &mockExecutionStore{}, &zoaMockMaestroClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/get_nodes", nil)
	req = mux.SetURLVars(req, map[string]string{"action": "get_nodes"})

	rr := httptest.NewRecorder()
	handler.Describe(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp zoa.TADescribeResponse
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "get_nodes", resp.Name)
	assert.Equal(t, "read", resp.Type)
	assert.Equal(t, "kube-api", resp.Scope)
	assert.Equal(t, "List all nodes in the target cluster", resp.Description)
}

func TestZoaHandler_Catalog(t *testing.T) {
	handler := newTestZoaHandler(t, &mockExecutionStore{}, &zoaMockMaestroClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions", nil)

	rr := httptest.NewRecorder()
	handler.Catalog(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, float64(1), resp["total"])
}

func TestZoaHandler_Create_UnknownParams(t *testing.T) {
	store := &mockExecutionStore{}
	mc := &zoaMockMaestroClient{}
	handler := newTestZoaHandler(t, store, mc)

	body := `{"target_cluster": "mc01", "jira": "ROSAENG-1234", "params": {"namespace": "kube-system"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/get_nodes/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "get_nodes"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var errResp map[string]interface{}
	err := json.NewDecoder(rr.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Equal(t, "invalid-params", errResp["code"])
	assert.Contains(t, errResp["reason"], "unknown parameter 'namespace'")
	assert.Contains(t, errResp["reason"], "node_selector")
}

func TestZoaHandler_Create_WriteCooldown(t *testing.T) {
	store := &mockExecutionStore{
		listFunc: func(ctx context.Context, accountID string, limit int, filter *zoa.ListFilter) ([]*zoa.Execution, error) {
			if filter != nil && filter.Action == "restart_pod" && filter.TargetCluster == "mc01" {
				return []*zoa.Execution{{ExecutionID: "recent-exec"}}, nil
			}
			return nil, nil
		},
	}
	mc := &zoaMockMaestroClient{}

	dir := t.TempDir()
	writeTemplateContent := `name: restart_pod
scope: kube-api
type: write
description: Restart a pod
write_cooldown_seconds: 300
rbac:
  cluster_scoped: true
  rules:
    - apiGroups: [""]
      resources: ["pods"]
      verbs: ["delete"]
script: |
  kubectl delete pod test
`
	require.NoError(t, os.WriteFile(dir+"/restart_pod.yaml", []byte(writeTemplateContent), 0644))
	registry := zoa.NewTemplateRegistry(testZoaLogger())
	require.NoError(t, registry.LoadFromDir(dir))

	handler := NewZoaHandler(store, registry, mc, &mockS3Client{}, ZoaConfig{
		BucketName: "test-bucket",
		JobConfig:  testJobConfig(),
	}, testZoaLogger())

	body := `{"target_cluster": "mc01", "jira": "ROSAENG-1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/restart_pod/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "restart_pod"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusTooManyRequests, rr.Code)
	var errResp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&errResp))
	assert.Equal(t, "write-cooldown", errResp["code"])
}

func TestZoaHandler_Create_WriteCooldown_ForceBypass(t *testing.T) {
	store := &mockExecutionStore{
		listFunc: func(ctx context.Context, accountID string, limit int, filter *zoa.ListFilter) ([]*zoa.Execution, error) {
			if filter != nil && filter.Action == "restart_pod" {
				return []*zoa.Execution{{ExecutionID: "recent-exec"}}, nil
			}
			return nil, nil
		},
	}
	mc := &zoaMockMaestroClient{}

	dir := t.TempDir()
	content := `name: restart_pod
scope: kube-api
type: write
description: Restart a pod
write_cooldown_seconds: 300
rbac:
  cluster_scoped: true
  rules:
    - apiGroups: [""]
      resources: ["pods"]
      verbs: ["delete"]
script: |
  kubectl delete pod test
`
	require.NoError(t, os.WriteFile(dir+"/restart_pod.yaml", []byte(content), 0644))
	registry := zoa.NewTemplateRegistry(testZoaLogger())
	require.NoError(t, registry.LoadFromDir(dir))

	handler := NewZoaHandler(store, registry, mc, &mockS3Client{}, ZoaConfig{
		BucketName: "test-bucket",
		JobConfig:  testJobConfig(),
	}, testZoaLogger())

	body := `{"target_cluster": "mc01", "jira": "ROSAENG-1234", "force": true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/restart_pod/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "restart_pod"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusAccepted, rr.Code)
}

func TestZoaHandler_Create_MaxConcurrent(t *testing.T) {
	callCount := 0
	store := &mockExecutionStore{
		listFunc: func(ctx context.Context, accountID string, limit int, filter *zoa.ListFilter) ([]*zoa.Execution, error) {
			callCount++
			if filter != nil && filter.Status == string(zoa.StatusRunning) {
				execs := make([]*zoa.Execution, 10)
				for i := range execs {
					execs[i] = &zoa.Execution{ExecutionID: "running-" + string(rune('0'+i))}
				}
				return execs, nil
			}
			return nil, nil
		},
	}
	mc := &zoaMockMaestroClient{}

	cfg := testJobConfig()
	cfg.MaxConcurrentPerTarget = 10

	handler := NewZoaHandler(store, testTemplateRegistry(t), mc, &mockS3Client{}, ZoaConfig{
		BucketName: "test-bucket",
		JobConfig:  cfg,
	}, testZoaLogger())

	body := `{"target_cluster": "mc01", "jira": "ROSAENG-1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/get_nodes/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "get_nodes"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusTooManyRequests, rr.Code)
	var errResp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&errResp))
	assert.Equal(t, "max-concurrent", errResp["code"])
	assert.Contains(t, errResp["reason"].(string), "10 active executions")
}

func TestZoaHandler_Create_MaxConcurrent_ForceBypass(t *testing.T) {
	store := &mockExecutionStore{
		listFunc: func(ctx context.Context, accountID string, limit int, filter *zoa.ListFilter) ([]*zoa.Execution, error) {
			if filter != nil && filter.Status == string(zoa.StatusRunning) {
				return make([]*zoa.Execution, 10), nil
			}
			return nil, nil
		},
	}
	mc := &zoaMockMaestroClient{}

	cfg := testJobConfig()
	cfg.MaxConcurrentPerTarget = 10

	handler := NewZoaHandler(store, testTemplateRegistry(t), mc, &mockS3Client{}, ZoaConfig{
		BucketName: "test-bucket",
		JobConfig:  cfg,
	}, testZoaLogger())

	body := `{"target_cluster": "mc01", "jira": "ROSAENG-1234", "force": true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/get_nodes/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "get_nodes"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusAccepted, rr.Code)
}

func TestZoaHandler_Get_IncludeOutput(t *testing.T) {
	store := &mockExecutionStore{
		getFunc: func(ctx context.Context, executionID string) (*zoa.Execution, error) {
			return &zoa.Execution{
				ExecutionID:  "exec-123",
				Action:       "get_nodes",
				Status:       zoa.StatusSucceeded,
				OutputStatus: zoa.OutputStatusUploaded,
				OutputPath:   "exec-123/output.json",
			}, nil
		},
	}
	handler := newTestZoaHandler(t, store, &zoaMockMaestroClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/runs/exec-123?include=output", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "exec-123"})

	rr := httptest.NewRecorder()
	handler.Get(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp zoa.ExecutionResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.NotNil(t, resp.Output)
}

func TestZoaHandler_Get_NoInclude_NoOutput(t *testing.T) {
	store := &mockExecutionStore{
		getFunc: func(ctx context.Context, executionID string) (*zoa.Execution, error) {
			return &zoa.Execution{
				ExecutionID:  "exec-123",
				Action:       "get_nodes",
				Status:       zoa.StatusSucceeded,
				OutputStatus: zoa.OutputStatusUploaded,
				OutputPath:   "exec-123/output.json",
			}, nil
		},
	}
	handler := newTestZoaHandler(t, store, &zoaMockMaestroClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/runs/exec-123", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "exec-123"})

	rr := httptest.NewRecorder()
	handler.Get(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp zoa.ExecutionResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Nil(t, resp.Output)
	assert.Empty(t, resp.Logs)
}

func TestZoaHandler_Get_IncludeLogs(t *testing.T) {
	store := &mockExecutionStore{
		getFunc: func(ctx context.Context, executionID string) (*zoa.Execution, error) {
			return &zoa.Execution{
				ExecutionID:  "exec-123",
				Action:       "get_nodes",
				Status:       zoa.StatusSucceeded,
				OutputStatus: zoa.OutputStatusUploaded,
				OutputPath:   "exec-123/output.json",
			}, nil
		},
	}

	s3Mock := &mockS3Client{}
	handler := NewZoaHandler(store, testTemplateRegistry(t), &zoaMockMaestroClient{}, s3Mock, ZoaConfig{
		BucketName: "test-bucket",
		JobConfig:  testJobConfig(),
	}, testZoaLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/runs/exec-123?include=logs", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "exec-123"})

	rr := httptest.NewRecorder()
	handler.Get(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp zoa.ExecutionResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Nil(t, resp.Output)
	assert.NotEmpty(t, resp.Logs)
}

func TestZoaHandler_Get_IncludeOutputAndLogs(t *testing.T) {
	store := &mockExecutionStore{
		getFunc: func(ctx context.Context, executionID string) (*zoa.Execution, error) {
			return &zoa.Execution{
				ExecutionID:  "exec-123",
				Action:       "get_nodes",
				Status:       zoa.StatusSucceeded,
				OutputStatus: zoa.OutputStatusUploaded,
				OutputPath:   "exec-123/output.json",
			}, nil
		},
	}
	handler := newTestZoaHandler(t, store, &zoaMockMaestroClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/runs/exec-123?include=output,logs", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "exec-123"})

	rr := httptest.NewRecorder()
	handler.Get(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp zoa.ExecutionResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.NotNil(t, resp.Output)
	assert.NotEmpty(t, resp.Logs)
}

type mockAuditStore struct {
	recordFunc func(ctx context.Context, entry *zoa.AuditEntry) error
	listFunc   func(ctx context.Context, accountID string, limit int, filter *zoa.AuditFilter) ([]*zoa.AuditEntry, error)
}

func (m *mockAuditStore) Record(ctx context.Context, entry *zoa.AuditEntry) error {
	if m.recordFunc != nil {
		return m.recordFunc(ctx, entry)
	}
	return nil
}

func (m *mockAuditStore) List(ctx context.Context, accountID string, limit int, filter *zoa.AuditFilter) ([]*zoa.AuditEntry, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, accountID, limit, filter)
	}
	return nil, nil
}

func TestZoaHandler_AuditList(t *testing.T) {
	auditStore := &mockAuditStore{
		listFunc: func(ctx context.Context, accountID string, limit int, filter *zoa.AuditFilter) ([]*zoa.AuditEntry, error) {
			return []*zoa.AuditEntry{
				{ID: "a1", Method: "POST", Action: "get_nodes", StatusCode: 202},
				{ID: "a2", Method: "GET", Action: "", StatusCode: 200},
			}, nil
		},
	}

	handler := NewZoaHandler(&mockExecutionStore{}, testTemplateRegistry(t), &zoaMockMaestroClient{}, &mockS3Client{}, ZoaConfig{
		BucketName: "test-bucket",
		JobConfig:  testJobConfig(),
		AuditStore: auditStore,
	}, testZoaLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/audit", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.AuditList(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "AuditList", resp["kind"])
	assert.Equal(t, float64(2), resp["total"])
}

func TestZoaHandler_AuditList_Disabled(t *testing.T) {
	handler := NewZoaHandler(&mockExecutionStore{}, testTemplateRegistry(t), &zoaMockMaestroClient{}, &mockS3Client{}, ZoaConfig{
		BucketName: "test-bucket",
		JobConfig:  testJobConfig(),
		AuditStore: nil,
	}, testZoaLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/audit", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))

	rr := httptest.NewRecorder()
	handler.AuditList(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestZoaHandler_AuditList_WithMethodFilter(t *testing.T) {
	auditStore := &mockAuditStore{
		listFunc: func(ctx context.Context, accountID string, limit int, filter *zoa.AuditFilter) ([]*zoa.AuditEntry, error) {
			assert.Equal(t, "POST", filter.Method)
			return []*zoa.AuditEntry{
				{ID: "a1", Method: "POST", Action: "get_nodes", StatusCode: 202},
			}, nil
		},
	}

	handler := NewZoaHandler(&mockExecutionStore{}, testTemplateRegistry(t), &zoaMockMaestroClient{}, &mockS3Client{}, ZoaConfig{
		BucketName: "test-bucket",
		JobConfig:  testJobConfig(),
		AuditStore: auditStore,
	}, testZoaLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v0/trusted-actions/audit?method=POST", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.AuditList(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestZoaHandler_Create_InvalidJiraFormat(t *testing.T) {
	handler := newTestZoaHandler(t, &mockExecutionStore{}, &zoaMockMaestroClient{})

	body := `{"target_cluster": "mc01", "jira": "not-a-jira"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/get_nodes/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "get_nodes"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	var errResp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&errResp))
	assert.Equal(t, "invalid-jira", errResp["code"])
}

func TestZoaHandler_Create_DryRun(t *testing.T) {
	store := &mockExecutionStore{}
	mc := &zoaMockMaestroClient{}
	handler := newTestZoaHandler(t, store, mc)

	body := `{"target_cluster": "mc01", "jira": "ROSAENG-1234", "dry_run": true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/get_nodes/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "get_nodes"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/test"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusAccepted, rr.Code)

	var resp zoa.Execution
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.True(t, resp.DryRun)
	assert.Equal(t, "get_nodes", resp.Action)
}

func TestZoaHandler_Create_RecordsAudit(t *testing.T) {
	var recorded *zoa.AuditEntry
	auditStore := &mockAuditStore{
		recordFunc: func(ctx context.Context, entry *zoa.AuditEntry) error {
			if entry.Method == "POST" {
				recorded = entry
			}
			return nil
		},
	}

	store := &mockExecutionStore{}
	mc := &zoaMockMaestroClient{}

	handler := NewZoaHandler(store, testTemplateRegistry(t), mc, &mockS3Client{}, ZoaConfig{
		BucketName: "test-bucket",
		JobConfig:  testJobConfig(),
		AuditStore: auditStore,
	}, testZoaLogger())

	body := `{"target_cluster": "mc01", "jira": "ROSAENG-1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v0/trusted-actions/get_nodes/run", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"action": "get_nodes"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, "111222333444"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::111222333444:user/slopezma"))

	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	assert.Equal(t, http.StatusAccepted, rr.Code)
	require.NotNil(t, recorded)
	assert.Equal(t, "POST", recorded.Method)
	assert.Equal(t, 202, recorded.StatusCode)
	assert.Equal(t, "get_nodes", recorded.Action)
	assert.Equal(t, "mc01", recorded.TargetCluster)
	assert.Equal(t, "ROSAENG-1234", recorded.Jira)
	assert.Equal(t, "slopezma", recorded.Operator)
	assert.NotEmpty(t, recorded.ExecutionID)
	assert.Equal(t, string(zoa.ApprovalNotRequired), recorded.ApprovalState)
}

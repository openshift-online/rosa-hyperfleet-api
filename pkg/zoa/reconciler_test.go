package zoa

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	workv1 "open-cluster-management.io/api/work/v1"
)

type mockMaestroClient struct {
	getManifestWorkFunc    func(ctx context.Context, clusterName, name string) (*workv1.ManifestWork, error)
	deleteManifestWorkFunc func(ctx context.Context, clusterName, name string) error
}

func (m *mockMaestroClient) CreateConsumer(ctx context.Context, req *maestro.ConsumerCreateRequest) (*maestro.Consumer, error) {
	return nil, nil
}
func (m *mockMaestroClient) ListConsumers(ctx context.Context, page, size int) (*maestro.ConsumerList, error) {
	return nil, nil
}
func (m *mockMaestroClient) GetConsumer(ctx context.Context, id string) (*maestro.Consumer, error) {
	return nil, nil
}
func (m *mockMaestroClient) ListResourceBundles(ctx context.Context, page, size int, search, orderBy, fields string) (*maestro.ResourceBundleList, error) {
	return nil, nil
}
func (m *mockMaestroClient) GetResourceBundle(ctx context.Context, id string) (*maestro.ResourceBundle, error) {
	return nil, nil
}
func (m *mockMaestroClient) DeleteResourceBundle(ctx context.Context, id string) error {
	return nil
}
func (m *mockMaestroClient) CreateManifestWork(ctx context.Context, clusterName string, manifestWork *workv1.ManifestWork) (*workv1.ManifestWork, error) {
	return nil, nil
}
func (m *mockMaestroClient) GetManifestWork(ctx context.Context, clusterName, name string) (*workv1.ManifestWork, error) {
	if m.getManifestWorkFunc != nil {
		return m.getManifestWorkFunc(ctx, clusterName, name)
	}
	return nil, nil
}
func (m *mockMaestroClient) DeleteManifestWork(ctx context.Context, clusterName, name string) error {
	if m.deleteManifestWorkFunc != nil {
		return m.deleteManifestWorkFunc(ctx, clusterName, name)
	}
	return nil
}

type mockExecutionStore struct {
	createFunc             func(ctx context.Context, exec *Execution) error
	getFunc                func(ctx context.Context, executionID string) (*Execution, error)
	listFunc               func(ctx context.Context, accountID string, limit int, filter *ListFilter) ([]*Execution, error)
	updateStatusFunc       func(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int) error
	updateCompletionFunc   func(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int, runnerSeconds int, uploadSeconds int, outputStatus OutputStatus) error
	updateMWNameFunc       func(ctx context.Context, executionID, mwName string) error
	listPendingFunc        func(ctx context.Context) ([]*Execution, error)
}

func (m *mockExecutionStore) Create(ctx context.Context, exec *Execution) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, exec)
	}
	return nil
}
func (m *mockExecutionStore) Get(ctx context.Context, executionID string) (*Execution, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, executionID)
	}
	return nil, nil
}
func (m *mockExecutionStore) List(ctx context.Context, accountID string, limit int, filter *ListFilter) ([]*Execution, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, accountID, limit, filter)
	}
	return nil, nil
}
func (m *mockExecutionStore) UpdateStatus(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int) error {
	if m.updateStatusFunc != nil {
		return m.updateStatusFunc(ctx, executionID, status, completedAt, duration)
	}
	return nil
}
func (m *mockExecutionStore) UpdateCompletion(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int, runnerSeconds int, uploadSeconds int, outputStatus OutputStatus) error {
	if m.updateCompletionFunc != nil {
		return m.updateCompletionFunc(ctx, executionID, status, completedAt, duration, runnerSeconds, uploadSeconds, outputStatus)
	}
	return nil
}
func (m *mockExecutionStore) UpdateManifestWorkName(ctx context.Context, executionID, mwName string) error {
	if m.updateMWNameFunc != nil {
		return m.updateMWNameFunc(ctx, executionID, mwName)
	}
	return nil
}
func (m *mockExecutionStore) ListPending(ctx context.Context) ([]*Execution, error) {
	if m.listPendingFunc != nil {
		return m.listPendingFunc(ctx)
	}
	return nil, nil
}

func reconcilerLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func defaultJobConfig() *JobConfig {
	return &JobConfig{
		ExecutionTimeoutSeconds: 1800,
		TTLSeconds:              3600,
	}
}

func TestReconcileExecution_PendingToRunning(t *testing.T) {
	var updatedStatus ExecutionStatus
	store := &mockExecutionStore{
		updateStatusFunc: func(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int) error {
			updatedStatus = status
			return nil
		},
	}

	mw := &workv1.ManifestWork{
		Status: workv1.ManifestWorkStatus{
			Conditions: []metav1.Condition{
				{Type: "Applied", Status: "True"},
			},
		},
	}

	maestroClient := &mockMaestroClient{
		getManifestWorkFunc: func(ctx context.Context, clusterName, name string) (*workv1.ManifestWork, error) {
			return mw, nil
		},
	}

	r := NewReconciler(store, nil, maestroClient, defaultJobConfig(), 10*time.Second, reconcilerLogger())

	exec := &Execution{
		ExecutionID:      "exec-1",
		TargetCluster:    "mc01",
		ManifestWorkName: "zoa-exec-1",
		Status:           StatusPending,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}

	r.reconcileExecution(context.Background(), exec)

	assert.Equal(t, StatusRunning, updatedStatus)
}

func TestReconcileExecution_FullyCompleted(t *testing.T) {
	var deletedCluster, deletedName string
	var completionStatus ExecutionStatus
	var completionOutputStatus OutputStatus

	store := &mockExecutionStore{
		updateCompletionFunc: func(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int, runnerSeconds int, uploadSeconds int, outputStatus OutputStatus) error {
			completionStatus = status
			completionOutputStatus = outputStatus
			return nil
		},
	}

	now := time.Now().UTC()
	int64One := int64(1)
	runnerStart := now.Add(-30 * time.Second).Format(time.RFC3339)
	runnerComplete := now.Add(-15 * time.Second).Format(time.RFC3339)
	uploadComplete := now.Add(-5 * time.Second).Format(time.RFC3339)

	mw := &workv1.ManifestWork{
		Status: workv1.ManifestWorkStatus{
			ResourceStatus: workv1.ManifestResourceStatus{
				Manifests: []workv1.ManifestCondition{
					{
						StatusFeedbacks: workv1.StatusFeedbackResult{
							Values: []workv1.FeedbackValue{
								{Name: "taSucceeded", Value: workv1.FieldValue{Integer: &int64One}},
								{Name: "runnerStartTime", Value: workv1.FieldValue{String: &runnerStart}},
								{Name: "runnerCompletionTime", Value: workv1.FieldValue{String: &runnerComplete}},
							},
						},
					},
					{
						StatusFeedbacks: workv1.StatusFeedbackResult{
							Values: []workv1.FeedbackValue{
								{Name: "uploadSucceeded", Value: workv1.FieldValue{Integer: &int64One}},
								{Name: "uploadCompletionTime", Value: workv1.FieldValue{String: &uploadComplete}},
							},
						},
					},
				},
			},
		},
	}

	maestroClient := &mockMaestroClient{
		getManifestWorkFunc: func(ctx context.Context, clusterName, name string) (*workv1.ManifestWork, error) {
			return mw, nil
		},
		deleteManifestWorkFunc: func(ctx context.Context, clusterName, name string) error {
			deletedCluster = clusterName
			deletedName = name
			return nil
		},
	}

	r := NewReconciler(store, nil, maestroClient, defaultJobConfig(), 10*time.Second, reconcilerLogger())

	exec := &Execution{
		ExecutionID:      "exec-2",
		TargetCluster:    "mc01",
		ManifestWorkName: "zoa-exec-2",
		Status:           StatusRunning,
		CreatedAt:        now.Add(-60 * time.Second).Format(time.RFC3339),
	}

	r.reconcileExecution(context.Background(), exec)

	assert.Equal(t, "mc01", deletedCluster)
	assert.Equal(t, "zoa-exec-2", deletedName)
	assert.Equal(t, StatusSucceeded, completionStatus)
	assert.Equal(t, OutputStatusUploaded, completionOutputStatus)
}

func TestReconcileExecution_Timeout(t *testing.T) {
	var deletedMW bool
	var statusUpdated ExecutionStatus

	store := &mockExecutionStore{
		updateStatusFunc: func(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int) error {
			statusUpdated = status
			return nil
		},
	}

	maestroClient := &mockMaestroClient{
		deleteManifestWorkFunc: func(ctx context.Context, clusterName, name string) error {
			deletedMW = true
			return nil
		},
	}

	// Total timeout = exec(60) + upload(120 default) + dispatch(120) = 300s
	cfg := &JobConfig{ExecutionTimeoutSeconds: 60}
	r := NewReconciler(store, nil, maestroClient, cfg, 10*time.Second, reconcilerLogger())

	exec := &Execution{
		ExecutionID:      "exec-timeout",
		TargetCluster:    "mc01",
		ManifestWorkName: "zoa-exec-timeout",
		Status:           StatusRunning,
		CreatedAt:        time.Now().UTC().Add(-301 * time.Second).Format(time.RFC3339),
	}

	r.reconcileExecution(context.Background(), exec)

	assert.True(t, deletedMW)
	assert.Equal(t, StatusTimedOut, statusUpdated)
}

func TestReconcileExecution_MWNotFound(t *testing.T) {
	store := &mockExecutionStore{
		updateStatusFunc: func(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int) error {
			t.Fatal("should not update status when MW is nil")
			return nil
		},
	}

	maestroClient := &mockMaestroClient{
		getManifestWorkFunc: func(ctx context.Context, clusterName, name string) (*workv1.ManifestWork, error) {
			return nil, nil
		},
	}

	r := NewReconciler(store, nil, maestroClient, defaultJobConfig(), 10*time.Second, reconcilerLogger())

	exec := &Execution{
		ExecutionID:      "exec-mw-nil",
		TargetCluster:    "mc01",
		ManifestWorkName: "zoa-exec-mw-nil",
		Status:           StatusPending,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}

	r.reconcileExecution(context.Background(), exec)
}

func TestReconcileExecution_RBDeletionFails(t *testing.T) {
	var completionCalled bool

	store := &mockExecutionStore{
		updateCompletionFunc: func(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int, runnerSeconds int, uploadSeconds int, outputStatus OutputStatus) error {
			completionCalled = true
			return nil
		},
	}

	int64One := int64(1)
	mw := &workv1.ManifestWork{
		Status: workv1.ManifestWorkStatus{
			ResourceStatus: workv1.ManifestResourceStatus{
				Manifests: []workv1.ManifestCondition{
					{
						StatusFeedbacks: workv1.StatusFeedbackResult{
							Values: []workv1.FeedbackValue{
								{Name: "taSucceeded", Value: workv1.FieldValue{Integer: &int64One}},
							},
						},
					},
					{
						StatusFeedbacks: workv1.StatusFeedbackResult{
							Values: []workv1.FeedbackValue{
								{Name: "uploadSucceeded", Value: workv1.FieldValue{Integer: &int64One}},
							},
						},
					},
				},
			},
		},
	}

	maestroClient := &mockMaestroClient{
		getManifestWorkFunc: func(ctx context.Context, clusterName, name string) (*workv1.ManifestWork, error) {
			return mw, nil
		},
		deleteManifestWorkFunc: func(ctx context.Context, clusterName, name string) error {
			return errors.New("network timeout")
		},
	}

	r := NewReconciler(store, nil, maestroClient, defaultJobConfig(), 10*time.Second, reconcilerLogger())

	exec := &Execution{
		ExecutionID:      "exec-rb-fail",
		TargetCluster:    "mc01",
		ManifestWorkName: "zoa-exec-rb-fail",
		Status:           StatusRunning,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}

	r.reconcileExecution(context.Background(), exec)

	assert.False(t, completionCalled, "should not update status when RB deletion fails")
}

func TestParseManifestWorkStatus_AllFeedback(t *testing.T) {
	int64One := int64(1)
	start := "2026-06-01T10:00:00Z"
	runnerEnd := "2026-06-01T10:00:15Z"
	uploadEnd := "2026-06-01T10:00:25Z"

	mw := &workv1.ManifestWork{
		Status: workv1.ManifestWorkStatus{
			ResourceStatus: workv1.ManifestResourceStatus{
				Manifests: []workv1.ManifestCondition{
					{
						StatusFeedbacks: workv1.StatusFeedbackResult{
							Values: []workv1.FeedbackValue{
								{Name: "taSucceeded", Value: workv1.FieldValue{Integer: &int64One}},
								{Name: "runnerStartTime", Value: workv1.FieldValue{String: &start}},
								{Name: "runnerCompletionTime", Value: workv1.FieldValue{String: &runnerEnd}},
							},
						},
					},
					{
						StatusFeedbacks: workv1.StatusFeedbackResult{
							Values: []workv1.FeedbackValue{
								{Name: "uploadSucceeded", Value: workv1.FieldValue{Integer: &int64One}},
								{Name: "uploadCompletionTime", Value: workv1.FieldValue{String: &uploadEnd}},
							},
						},
					},
				},
			},
		},
	}

	r := &Reconciler{logger: reconcilerLogger()}
	result := r.parseManifestWorkStatus(mw)

	assert.True(t, result.taSucceeded)
	assert.True(t, result.uploadSucceeded)
	assert.True(t, result.fullyCompleted())
	assert.Equal(t, StatusSucceeded, result.taStatus())
	assert.Equal(t, OutputStatusUploaded, result.outputStatus())
}

func TestParseManifestWorkStatus_TAFailedUploadSucceeded(t *testing.T) {
	int64One := int64(1)

	mw := &workv1.ManifestWork{
		Status: workv1.ManifestWorkStatus{
			ResourceStatus: workv1.ManifestResourceStatus{
				Manifests: []workv1.ManifestCondition{
					{
						StatusFeedbacks: workv1.StatusFeedbackResult{
							Values: []workv1.FeedbackValue{
								{Name: "taFailed", Value: workv1.FieldValue{Integer: &int64One}},
							},
						},
					},
					{
						StatusFeedbacks: workv1.StatusFeedbackResult{
							Values: []workv1.FeedbackValue{
								{Name: "uploadSucceeded", Value: workv1.FieldValue{Integer: &int64One}},
							},
						},
					},
				},
			},
		},
	}

	r := &Reconciler{logger: reconcilerLogger()}
	result := r.parseManifestWorkStatus(mw)

	assert.True(t, result.taFailed)
	assert.True(t, result.uploadSucceeded)
	assert.True(t, result.fullyCompleted())
	assert.Equal(t, StatusFailed, result.taStatus())
	assert.Equal(t, OutputStatusUploaded, result.outputStatus())
}

func TestParseManifestWorkStatus_AppliedOnly(t *testing.T) {
	mw := &workv1.ManifestWork{
		Status: workv1.ManifestWorkStatus{
			Conditions: []metav1.Condition{
				{Type: "Applied", Status: "True"},
			},
		},
	}

	r := &Reconciler{logger: reconcilerLogger()}
	result := r.parseManifestWorkStatus(mw)

	assert.True(t, result.applied)
	assert.False(t, result.fullyCompleted())
}

func TestParseManifestWorkStatus_NoFeedbackNoCondition(t *testing.T) {
	mw := &workv1.ManifestWork{
		Status: workv1.ManifestWorkStatus{},
	}

	r := &Reconciler{logger: reconcilerLogger()}
	result := r.parseManifestWorkStatus(mw)

	assert.False(t, result.applied)
	assert.False(t, result.fullyCompleted())
	assert.False(t, result.taSucceeded)
	assert.False(t, result.taFailed)
}

func TestJobResult_ComputeDurations(t *testing.T) {
	jr := &jobResult{
		runnerStartTime:      "2026-06-01T10:00:00Z",
		runnerCompletionTime: "2026-06-01T10:00:13Z",
		uploadCompletionTime: "2026-06-01T10:00:21Z",
	}

	assert.Equal(t, 13, jr.computeRunnerSeconds())
	assert.Equal(t, 8, jr.computeUploadSeconds())
}

func TestJobResult_ComputeDurations_InvalidTimes(t *testing.T) {
	jr := &jobResult{
		runnerStartTime:      "",
		runnerCompletionTime: "invalid",
		uploadCompletionTime: "",
	}

	assert.Equal(t, 0, jr.computeRunnerSeconds())
	assert.Equal(t, 0, jr.computeUploadSeconds())
}

func TestIsTimedOut(t *testing.T) {
	// Total timeout = exec(60) + upload(120 default) + dispatch(120) = 300s
	cfg := &JobConfig{ExecutionTimeoutSeconds: 60}
	r := NewReconciler(nil, nil, nil, cfg, 10*time.Second, reconcilerLogger())

	t.Run("When execution is within timeout it should not be timed out", func(t *testing.T) {
		exec := &Execution{
			CreatedAt: time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339),
		}
		assert.False(t, r.isTimedOut(exec))
	})

	t.Run("When execution exceeds timeout it should be timed out", func(t *testing.T) {
		exec := &Execution{
			CreatedAt: time.Now().UTC().Add(-301 * time.Second).Format(time.RFC3339),
		}
		assert.True(t, r.isTimedOut(exec))
	})

	t.Run("When execution is exactly at timeout boundary it should not be timed out", func(t *testing.T) {
		exec := &Execution{
			CreatedAt: time.Now().UTC().Add(-299 * time.Second).Format(time.RFC3339),
		}
		assert.False(t, r.isTimedOut(exec))
	})

	t.Run("When createdAt is invalid it should not be timed out", func(t *testing.T) {
		exec := &Execution{
			CreatedAt: "invalid-timestamp",
		}
		assert.False(t, r.isTimedOut(exec))
	})
}

func TestTimeoutForExecution_PerTAOverride(t *testing.T) {
	registry := NewTemplateRegistry(reconcilerLogger())
	registry.templates["slow_action"] = &TATemplate{
		Name:           "slow_action",
		TimeoutSeconds: 3600,
	}
	registry.templates["fast_action"] = &TATemplate{
		Name:           "fast_action",
		TimeoutSeconds: 0,
	}

	// upload=120 (default), dispatch=120 (const)
	cfg := &JobConfig{ExecutionTimeoutSeconds: 1800}
	r := NewReconciler(nil, registry, nil, cfg, 10*time.Second, reconcilerLogger())

	t.Run("When TA has custom timeout it should use TA timeout", func(t *testing.T) {
		exec := &Execution{Action: "slow_action"}
		// 3600 + 120(upload) + 120(dispatch) = 3840s
		assert.Equal(t, 3840*time.Second, r.timeoutForExecution(exec))
	})

	t.Run("When TA has zero timeout it should use default", func(t *testing.T) {
		exec := &Execution{Action: "fast_action"}
		// 1800 + 120(upload) + 120(dispatch) = 2040s
		assert.Equal(t, 2040*time.Second, r.timeoutForExecution(exec))
	})

	t.Run("When TA is not in registry it should use default", func(t *testing.T) {
		exec := &Execution{Action: "unknown_action"}
		// 1800 + 120(upload) + 120(dispatch) = 2040s
		assert.Equal(t, 2040*time.Second, r.timeoutForExecution(exec))
	})
}

func TestReconcileExecution_EmptyManifestWorkName(t *testing.T) {
	store := &mockExecutionStore{
		updateStatusFunc: func(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int) error {
			t.Fatal("should not update status when ManifestWorkName is empty")
			return nil
		},
	}

	r := NewReconciler(store, nil, nil, defaultJobConfig(), 10*time.Second, reconcilerLogger())

	exec := &Execution{
		ExecutionID:      "exec-no-mw",
		TargetCluster:    "mc01",
		ManifestWorkName: "",
		Status:           StatusPending,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}

	r.reconcileExecution(context.Background(), exec)
}

func TestReconcilePending_ListError(t *testing.T) {
	store := &mockExecutionStore{
		listPendingFunc: func(ctx context.Context) ([]*Execution, error) {
			return nil, errors.New("dynamo error")
		},
	}

	r := NewReconciler(store, nil, nil, defaultJobConfig(), 10*time.Second, reconcilerLogger())
	r.reconcilePending(context.Background())
}

func TestReconcilePending_NoExecutions(t *testing.T) {
	store := &mockExecutionStore{
		listPendingFunc: func(ctx context.Context) ([]*Execution, error) {
			return []*Execution{}, nil
		},
	}

	r := NewReconciler(store, nil, nil, defaultJobConfig(), 10*time.Second, reconcilerLogger())
	r.reconcilePending(context.Background())
}

func TestReconcilePending_MultipleExecutions(t *testing.T) {
	reconciled := make([]string, 0)

	mw := &workv1.ManifestWork{
		Status: workv1.ManifestWorkStatus{
			Conditions: []metav1.Condition{
				{Type: "Applied", Status: "True"},
			},
		},
	}

	store := &mockExecutionStore{
		listPendingFunc: func(ctx context.Context) ([]*Execution, error) {
			return []*Execution{
				{ExecutionID: "exec-a", TargetCluster: "mc01", ManifestWorkName: "zoa-a", Status: StatusPending, CreatedAt: time.Now().UTC().Format(time.RFC3339)},
				{ExecutionID: "exec-b", TargetCluster: "mc02", ManifestWorkName: "zoa-b", Status: StatusPending, CreatedAt: time.Now().UTC().Format(time.RFC3339)},
			}, nil
		},
		updateStatusFunc: func(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int) error {
			reconciled = append(reconciled, executionID)
			return nil
		},
	}

	maestroClient := &mockMaestroClient{
		getManifestWorkFunc: func(ctx context.Context, clusterName, name string) (*workv1.ManifestWork, error) {
			return mw, nil
		},
	}

	r := NewReconciler(store, nil, maestroClient, defaultJobConfig(), 10*time.Second, reconcilerLogger())
	r.reconcilePending(context.Background())

	require.Len(t, reconciled, 2)
	assert.Contains(t, reconciled, "exec-a")
	assert.Contains(t, reconciled, "exec-b")
}

func TestHandleTimeout_RBDeletionFails(t *testing.T) {
	var statusUpdated bool
	store := &mockExecutionStore{
		updateStatusFunc: func(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int) error {
			statusUpdated = true
			return nil
		},
	}

	maestroClient := &mockMaestroClient{
		deleteManifestWorkFunc: func(ctx context.Context, clusterName, name string) error {
			return errors.New("timeout connecting to maestro")
		},
	}

	cfg := &JobConfig{ExecutionTimeoutSeconds: 60}
	r := NewReconciler(store, nil, maestroClient, cfg, 10*time.Second, reconcilerLogger())

	exec := &Execution{
		ExecutionID:      "exec-timeout-rb-fail",
		TargetCluster:    "mc01",
		ManifestWorkName: "zoa-exec-timeout-rb-fail",
		Status:           StatusRunning,
		CreatedAt:        time.Now().UTC().Add(-120 * time.Second).Format(time.RFC3339),
	}

	r.handleTimeout(context.Background(), exec)

	assert.False(t, statusUpdated, "should not update status when RB deletion fails")
}

func TestDeleteResourceBundle_AlreadyGone(t *testing.T) {
	maestroClient := &mockMaestroClient{
		deleteManifestWorkFunc: func(ctx context.Context, clusterName, name string) error {
			return &maestro.Error{Code: "404", Reason: "not found"}
		},
	}

	r := NewReconciler(nil, nil, maestroClient, defaultJobConfig(), 10*time.Second, reconcilerLogger())

	exec := &Execution{
		ExecutionID:      "exec-gone",
		TargetCluster:    "mc01",
		ManifestWorkName: "zoa-exec-gone",
	}

	err := r.deleteResourceBundle(context.Background(), exec)
	assert.NoError(t, err)
}

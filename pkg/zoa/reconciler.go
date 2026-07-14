package zoa

import (
	"context"
	"log/slog"
	"time"

	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	workv1 "open-cluster-management.io/api/work/v1"
)

// Reconciler periodically checks pending/running TA executions and updates their
// status by inspecting Maestro ManifestWork feedback via gRPC.
// On terminal states (succeeded, failed, timeout), it deletes the ResourceBundle
// BEFORE updating status, preventing stale RBs if the status update were to fail.
type Reconciler struct {
	store         ExecutionStore
	registry      *TemplateRegistry
	maestroClient maestro.ClientInterface
	jobConfig     *JobConfig
	logger        *slog.Logger
	interval      time.Duration
}

func NewReconciler(
	store ExecutionStore,
	registry *TemplateRegistry,
	maestroClient maestro.ClientInterface,
	jobConfig *JobConfig,
	interval time.Duration,
	logger *slog.Logger,
) *Reconciler {
	return &Reconciler{
		store:         store,
		registry:      registry,
		maestroClient: maestroClient,
		jobConfig:     jobConfig,
		logger:        logger,
		interval:      interval,
	}
}

func (r *Reconciler) Run(ctx context.Context) {
	r.logger.Info("ZOA reconciler started", "interval", r.interval, "default_timeout_seconds", r.jobConfig.ExecutionTimeoutSeconds)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("ZOA reconciler stopped")
			return
		case <-ticker.C:
			r.reconcilePending(ctx)
		}
	}
}

func (r *Reconciler) reconcilePending(ctx context.Context) {
	executions, err := r.store.ListPending(ctx)
	if err != nil {
		r.logger.Error("failed to list pending executions", "error", err)
		return
	}

	if len(executions) == 0 {
		return
	}

	r.logger.Debug("reconciling pending executions", "count", len(executions))

	for _, exec := range executions {
		r.reconcileExecution(ctx, exec)
	}
}

func (r *Reconciler) reconcileExecution(ctx context.Context, exec *Execution) {
	if exec.ManifestWorkName == "" || exec.TargetCluster == "" {
		return
	}

	if r.isTimedOut(exec) {
		r.handleTimeout(ctx, exec)
		return
	}

	mw, err := r.maestroClient.GetManifestWork(ctx, exec.TargetCluster, exec.ManifestWorkName)
	if err != nil {
		r.logger.Error("failed to get manifestwork from maestro",
			"execution_id", exec.ExecutionID,
			"manifest_work", exec.ManifestWorkName,
			"target_cluster", exec.TargetCluster,
			"error", err,
		)
		return
	}

	if mw == nil {
		return
	}

	result := r.parseManifestWorkStatus(mw)

	if result.fullyCompleted() {
		r.handleCompletion(ctx, exec, result)
		return
	}

	if result.applied && exec.Status == StatusPending {
		if err := r.store.UpdateStatus(ctx, exec.ExecutionID, StatusRunning, "", 0); err != nil {
			r.logger.Error("failed to update execution status to running",
				"execution_id", exec.ExecutionID,
				"error", err,
			)
			return
		}
		r.logger.Info("execution status updated",
			"execution_id", exec.ExecutionID,
			"status", "running",
		)
	}
}

// handleTimeout deletes the ResourceBundle FIRST, then marks as timed_out.
// If RB deletion fails, status stays pending/running so the reconciler retries.
func (r *Reconciler) handleTimeout(ctx context.Context, exec *Execution) {
	timeout := r.timeoutForExecution(exec)
	createdAt, _ := time.Parse(time.RFC3339, exec.CreatedAt)

	r.logger.Warn("execution exceeded timeout, cleaning up",
		"execution_id", exec.ExecutionID,
		"age", time.Since(createdAt).String(),
		"timeout", timeout.String(),
	)

	if err := r.deleteResourceBundle(ctx, exec); err != nil {
		return
	}

	now := time.Now().UTC()
	duration := int(now.Sub(createdAt).Seconds())
	if err := r.store.UpdateStatus(ctx, exec.ExecutionID, StatusTimedOut, now.Format(time.RFC3339), duration); err != nil {
		r.logger.Error("resource bundle deleted but failed to update status to timed_out — will not retry RB deletion",
			"execution_id", exec.ExecutionID,
			"error", err,
		)
		return
	}

	r.logger.Info("execution marked as timed_out",
		"execution_id", exec.ExecutionID,
		"duration_seconds", duration,
	)
}

// handleCompletion deletes the ResourceBundle FIRST, then updates terminal status.
// Computes durations from Job timestamps reported via Maestro feedback.
func (r *Reconciler) handleCompletion(ctx context.Context, exec *Execution, result *jobResult) {
	if err := r.deleteResourceBundle(ctx, exec); err != nil {
		return
	}

	now := time.Now().UTC()
	createdAt, _ := time.Parse(time.RFC3339, exec.CreatedAt)
	totalDuration := int(now.Sub(createdAt).Seconds())

	runnerSeconds := result.computeRunnerSeconds()
	uploadSeconds := result.computeUploadSeconds()

	status := result.taStatus()
	if status == "" {
		status = StatusFailed
	}
	outputStatus := result.outputStatus()

	if err := r.store.UpdateCompletion(ctx, exec.ExecutionID, status, now.Format(time.RFC3339), totalDuration, runnerSeconds, uploadSeconds, outputStatus); err != nil {
		r.logger.Error("resource bundle deleted but failed to update terminal status",
			"execution_id", exec.ExecutionID,
			"terminal_status", string(status),
			"error", err,
		)
		return
	}

	r.logger.Info("execution completed",
		"execution_id", exec.ExecutionID,
		"status", string(status),
		"output_status", string(outputStatus),
		"duration_seconds", totalDuration,
		"runner_seconds", runnerSeconds,
		"upload_seconds", uploadSeconds,
	)
}

// deleteResourceBundle removes the RB from Maestro. Returns nil on success or
// if the RB is already gone (idempotent). Returns error if deletion actually fails.
func (r *Reconciler) deleteResourceBundle(ctx context.Context, exec *Execution) error {
	err := r.maestroClient.DeleteManifestWork(ctx, exec.TargetCluster, exec.ManifestWorkName)
	if err != nil {
		if maestro.IsNotFound(err) {
			r.logger.Debug("resource bundle already deleted",
				"execution_id", exec.ExecutionID,
				"manifest_work", exec.ManifestWorkName,
			)
			return nil
		}
		r.logger.Error("failed to delete resource bundle — will retry next reconcile",
			"execution_id", exec.ExecutionID,
			"manifest_work", exec.ManifestWorkName,
			"error", err,
		)
		return err
	}

	r.logger.Info("resource bundle deleted",
		"execution_id", exec.ExecutionID,
		"manifest_work", exec.ManifestWorkName,
	)
	return nil
}

const dispatchBuffer = 120 // seconds — covers Maestro + MQTT + pod scheduling + image pull

func (r *Reconciler) timeoutForExecution(exec *Execution) time.Duration {
	execTimeout := r.jobConfig.ExecutionTimeoutSeconds
	if r.registry != nil {
		if tmpl, ok := r.registry.Get(exec.Action); ok && tmpl.TimeoutSeconds > 0 {
			execTimeout = tmpl.TimeoutSeconds
		}
	}
	uploadTimeout := r.jobConfig.UploadTimeoutSeconds
	if uploadTimeout == 0 {
		uploadTimeout = 120
	}
	return time.Duration(execTimeout+uploadTimeout+dispatchBuffer) * time.Second
}

func (r *Reconciler) isTimedOut(exec *Execution) bool {
	createdAt, err := time.Parse(time.RFC3339, exec.CreatedAt)
	if err != nil {
		return false
	}
	return time.Since(createdAt) > r.timeoutForExecution(exec)
}

// jobResult holds parsed completion info from ManifestWork feedback for both Jobs.
type jobResult struct {
	taSucceeded         bool
	taFailed            bool
	uploadSucceeded     bool
	uploadFailed        bool
	applied             bool
	runnerStartTime     string
	runnerCompletionTime string
	uploadCompletionTime string
}

func (jr *jobResult) taCompleted() bool {
	return jr.taSucceeded || jr.taFailed
}

func (jr *jobResult) uploadCompleted() bool {
	return jr.uploadSucceeded || jr.uploadFailed
}

func (jr *jobResult) fullyCompleted() bool {
	return jr.taCompleted() && jr.uploadCompleted()
}

func (jr *jobResult) taStatus() ExecutionStatus {
	if jr.taSucceeded {
		return StatusSucceeded
	}
	if jr.taFailed {
		return StatusFailed
	}
	return ""
}

func (jr *jobResult) outputStatus() OutputStatus {
	if jr.uploadSucceeded {
		return OutputStatusUploaded
	}
	if jr.uploadFailed {
		return OutputStatusFailed
	}
	return OutputStatusPending
}

func (jr *jobResult) computeRunnerSeconds() int {
	start, err1 := time.Parse(time.RFC3339, jr.runnerStartTime)
	end, err2 := time.Parse(time.RFC3339, jr.runnerCompletionTime)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(end.Sub(start).Seconds())
}

func (jr *jobResult) computeUploadSeconds() int {
	runnerEnd, err1 := time.Parse(time.RFC3339, jr.runnerCompletionTime)
	uploadEnd, err2 := time.Parse(time.RFC3339, jr.uploadCompletionTime)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(uploadEnd.Sub(runnerEnd).Seconds())
}

// parseManifestWorkStatus extracts Job status from both runner and uploader feedback.
func (r *Reconciler) parseManifestWorkStatus(mw *workv1.ManifestWork) *jobResult {
	result := &jobResult{}

	for _, resourceStatus := range mw.Status.ResourceStatus.Manifests {
		for _, value := range resourceStatus.StatusFeedbacks.Values {
			switch value.Name {
			case "taSucceeded":
				if value.Value.Integer != nil && *value.Value.Integer > 0 {
					result.taSucceeded = true
				}
			case "taFailed":
				if value.Value.Integer != nil && *value.Value.Integer > 0 {
					result.taFailed = true
				}
			case "uploadSucceeded":
				if value.Value.Integer != nil && *value.Value.Integer > 0 {
					result.uploadSucceeded = true
				}
			case "uploadFailed":
				if value.Value.Integer != nil && *value.Value.Integer > 0 {
					result.uploadFailed = true
				}
			case "runnerStartTime":
				if value.Value.String != nil {
					result.runnerStartTime = *value.Value.String
				}
			case "runnerCompletionTime":
				if value.Value.String != nil {
					result.runnerCompletionTime = *value.Value.String
				}
			case "uploadCompletionTime":
				if value.Value.String != nil {
					result.uploadCompletionTime = *value.Value.String
				}
			}
		}
	}

	if result.taCompleted() || result.uploadCompleted() {
		return result
	}

	for _, condition := range mw.Status.Conditions {
		if condition.Type == "Applied" && condition.Status == "True" {
			result.applied = true
			return result
		}
	}

	return result
}

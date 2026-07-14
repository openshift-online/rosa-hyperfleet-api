package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
	"github.com/openshift/rosa-hyperfleet-api/pkg/zoa"
)

var jiraTicketRegex = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)

// ZoaHandler handles ZOA Trusted Action endpoints.
type ZoaHandler struct {
	store         zoa.ExecutionStore
	auditStore    zoa.AuditStore
	registry      *zoa.TemplateRegistry
	maestroClient maestro.ClientInterface
	s3Client      S3Client
	bucketName    string
	jobConfig     *zoa.JobConfig
	logger        *slog.Logger
}

// S3Client provides operations for accessing S3 objects.
type S3Client interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// ZoaConfig holds configuration for the ZOA handler.
type ZoaConfig struct {
	BucketName string
	JobConfig  *zoa.JobConfig
	AuditStore zoa.AuditStore
}

// NewZoaHandler creates a new ZoaHandler.
func NewZoaHandler(
	store zoa.ExecutionStore,
	registry *zoa.TemplateRegistry,
	maestroClient maestro.ClientInterface,
	s3Client S3Client,
	cfg ZoaConfig,
	logger *slog.Logger,
) *ZoaHandler {
	return &ZoaHandler{
		store:         store,
		auditStore:    cfg.AuditStore,
		registry:      registry,
		maestroClient: maestroClient,
		s3Client:      s3Client,
		bucketName:    cfg.BucketName,
		jobConfig:     cfg.JobConfig,
		logger:        logger,
	}
}

// Create handles POST /api/v0/trusted-actions/{action}/run
func (h *ZoaHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	callerARN := middleware.GetCallerARN(ctx)
	action := mux.Vars(r)["action"]

	tmpl, ok := h.registry.Get(action)
	if !ok {
		h.writeError(w, http.StatusNotFound, "unknown-action", "Trusted action not found: "+action)
		return
	}

	var req zoa.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
		return
	}

	if req.TargetCluster == "" {
		h.recordAudit(ctx, r, accountID, callerARN, extractOperator(callerARN), http.StatusBadRequest, action, "", "", "", "")
		h.writeError(w, http.StatusBadRequest, "missing-target-cluster", "target_cluster is required")
		return
	}

	if req.Jira == "" {
		h.recordAudit(ctx, r, accountID, callerARN, extractOperator(callerARN), http.StatusBadRequest, action, req.TargetCluster, "", "", "")
		h.writeError(w, http.StatusBadRequest, "missing-jira", "jira is required for all trusted actions (e.g. ROSAENG-1234)")
		return
	}
	if !isValidJiraFormat(req.Jira) {
		h.recordAudit(ctx, r, accountID, callerARN, extractOperator(callerARN), http.StatusBadRequest, action, req.TargetCluster, "", req.Jira, "")
		h.writeError(w, http.StatusBadRequest, "invalid-jira", "jira does not have correct format; expected PROJECT-NUMBER (e.g. ROSAENG-1234)")
		return
	}

	cleanParams := make(map[string]string, len(req.Params))
	for k, v := range req.Params {
		if k != "" {
			cleanParams[k] = v
		}
	}

	if err := validateParams(tmpl, cleanParams); err != nil {
		h.recordAudit(ctx, r, accountID, callerARN, extractOperator(callerARN), http.StatusBadRequest, action, req.TargetCluster, "", req.Jira, "")
		h.writeError(w, http.StatusBadRequest, "invalid-params", err.Error())
		return
	}

	if tmpl.Type == "write" && !req.Force && !req.DryRun {
		cooldown := tmpl.WriteCooldownSeconds
		if cooldown == 0 {
			cooldown = h.jobConfig.WriteCooldownSeconds
		}
		if cooldown > 0 {
			if err := h.checkWriteCooldown(ctx, accountID, action, req.TargetCluster, cooldown); err != nil {
				h.recordAudit(ctx, r, accountID, callerARN, extractOperator(callerARN), http.StatusTooManyRequests, action, req.TargetCluster, "", req.Jira, "")
				h.writeError(w, http.StatusTooManyRequests, "write-cooldown", err.Error())
				return
			}
		}
	}

	if !req.DryRun && !req.Force {
		maxConcurrent := h.jobConfig.MaxConcurrentPerTarget
		if maxConcurrent <= 0 {
			maxConcurrent = 10
		}
		if err := h.checkMaxConcurrent(ctx, accountID, req.TargetCluster, maxConcurrent); err != nil {
			h.recordAudit(ctx, r, accountID, callerARN, extractOperator(callerARN), http.StatusTooManyRequests, action, req.TargetCluster, "", req.Jira, "")
			h.writeError(w, http.StatusTooManyRequests, "max-concurrent", err.Error())
			return
		}
	}

	originalAction := action
	originalType := tmpl.Type
	executedAction := ""

	if req.DryRun && tmpl.DryRunAction != "" {
		executedAction = tmpl.DryRunAction
		dryTmpl, ok := h.registry.Get(executedAction)
		if !ok {
			h.writeError(w, http.StatusInternalServerError, "dry-run-error", "dry_run_action '"+tmpl.DryRunAction+"' not found in registry")
			return
		}
		tmpl = dryTmpl
		action = executedAction
	}

	execID := uuid.New().String()
	operator := extractOperator(callerARN)

	exec := &zoa.Execution{
		ExecutionID:    execID,
		AccountID:      accountID,
		CallerARN:      callerARN,
		Operator:       operator,
		Action:         originalAction,
		ExecutedAction: executedAction,
		DryRun:         req.DryRun,
		Force:          req.Force,
		TargetCluster:  req.TargetCluster,
		Params:         cleanParams,
		Jira:           req.Jira,
		ApprovalState:  zoa.ApprovalNotRequired,
		Scope:          tmpl.Scope,
		Type:           originalType,
		Revision:       h.jobConfig.Revision,
		Status:         zoa.StatusPending,
		OutputStatus:   zoa.OutputStatusPending,
		OutputPath:     "s3://" + h.bucketName + "/" + execID + "/output.json",
	}

	if err := h.store.Create(ctx, exec); err != nil {
		h.logger.Error("failed to create execution record", "error", err, "execution_id", execID)
		h.writeError(w, http.StatusInternalServerError, "store-error", "Failed to create execution")
		return
	}

	renderCtx := zoa.RenderContext{
		ExecID:        execID,
		ActionName:    action,
		TargetCluster: req.TargetCluster,
		Namespace:     zoa.JobNamespace,
		OutputBucket:  h.bucketName,
		Operator:      operator,
		Revision:      h.jobConfig.Revision,
		Type:          tmpl.Type,
		Scope:         tmpl.Scope,
		Params:        req.Params,
		Config:        *h.jobConfig,
	}

	mw, err := zoa.BuildManifestWork(tmpl, renderCtx)
	if err != nil {
		h.logger.Error("failed to build manifestwork", "error", err, "execution_id", execID)
		_ = h.store.UpdateStatus(ctx, execID, zoa.StatusFailed, time.Now().UTC().Format(time.RFC3339), 0)
		h.writeError(w, http.StatusInternalServerError, "render-error", "Failed to build trusted action manifest")
		return
	}

	result, err := h.maestroClient.CreateManifestWork(ctx, req.TargetCluster, mw)
	if err != nil {
		h.logger.Error("failed to dispatch manifestwork", "error", err, "execution_id", execID)
		_ = h.store.UpdateStatus(ctx, execID, zoa.StatusFailed, time.Now().UTC().Format(time.RFC3339), 0)
		h.writeError(w, http.StatusBadGateway, "maestro-error", "Failed to dispatch trusted action")
		return
	}

	exec.ManifestWorkName = result.Name
	if err := h.store.UpdateManifestWorkName(ctx, execID, result.Name); err != nil {
		h.logger.Error("failed to update manifestwork name", "error", err, "execution_id", execID)
	}

	h.logger.Info("trusted action dispatched",
		"execution_id", execID,
		"action", action,
		"target_cluster", req.TargetCluster,
		"manifest_work", result.Name,
		"operator", operator,
		"scope", tmpl.Scope,
		"type", tmpl.Type,
	)

	h.recordAudit(ctx, r, accountID, callerARN, operator, http.StatusAccepted, originalAction, req.TargetCluster, execID, req.Jira, string(exec.ApprovalState))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(exec)
}

// Get handles GET /api/v0/trusted-actions/runs/{id}
func (h *ZoaHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	callerARN := middleware.GetCallerARN(ctx)
	operator := extractOperator(callerARN)
	execID := mux.Vars(r)["id"]

	exec, err := h.store.Get(ctx, execID)
	if err != nil {
		h.logger.Error("failed to get execution", "error", err, "execution_id", execID)
		h.writeError(w, http.StatusInternalServerError, "store-error", "Failed to retrieve execution")
		return
	}

	if exec == nil {
		h.writeError(w, http.StatusNotFound, "not-found", "Execution not found")
		return
	}

	h.recordAudit(ctx, r, accountID, callerARN, operator, http.StatusOK, "", "", execID, "", "")

	fields := parseInclude(r.URL.Query().Get("include"))

	response := &zoa.ExecutionResponse{
		Execution: exec,
	}

	if exec.Status == zoa.StatusSucceeded || exec.Status == zoa.StatusFailed || exec.Status == zoa.StatusTimedOut {
		if exec.OutputStatus == zoa.OutputStatusUploaded {
			if fields.output {
				outputURI := exec.OutputPath
				if outputURI == "" {
					outputURI = exec.ExecutionID + "/output.json"
				}
				output, err := h.fetchS3Content(ctx, outputURI)
				if err != nil {
					h.logger.Error("failed to fetch output from S3", "error", err, "uri", outputURI)
				} else if output != nil {
					var parsed interface{}
					if json.Unmarshal(output, &parsed) == nil {
						response.Output = parsed
					} else {
						response.Output = string(output)
					}
				}
			}

			if fields.logs {
				logsURI := strings.Replace(exec.OutputPath, "/output.json", "/execution.log", 1)
				if exec.OutputPath == "" {
					logsURI = exec.ExecutionID + "/execution.log"
				}
				logs, err := h.fetchS3Content(ctx, logsURI)
				if err != nil {
					h.logger.Error("failed to fetch logs from S3", "error", err, "uri", logsURI)
				}
				response.Logs = string(logs)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

// List handles GET /api/v0/trusted-actions/runs
func (h *ZoaHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	query := r.URL.Query()

	limit := 20
	if v := query.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	filter := &zoa.ListFilter{
		Status:        query.Get("status"),
		Action:        query.Get("action"),
		TargetCluster: query.Get("target"),
		Operator:      query.Get("operator"),
		Scope:         query.Get("scope"),
		Type:          query.Get("type"),
		OutputStatus:  query.Get("output_status"),
		ApprovalState: query.Get("approval_state"),
	}
	if v := query.Get("dry_run"); v == "true" {
		b := true
		filter.DryRun = &b
	} else if v == "false" {
		b := false
		filter.DryRun = &b
	}
	if v := query.Get("force"); v == "true" {
		b := true
		filter.Force = &b
	} else if v == "false" {
		b := false
		filter.Force = &b
	}

	if since := query.Get("since"); since != "" {
		if ts, err := parseSince(since); err == nil {
			filter.Since = ts
		}
	}

	executions, err := h.store.List(ctx, accountID, limit, filter)
	if err != nil {
		h.logger.Error("failed to list executions", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "store-error", "Failed to list executions")
		return
	}

	response := &zoa.ExecutionList{
		Items:   executions,
		Total:   len(executions),
		Page:    1,
		Limit:   limit,
		HasMore: len(executions) >= limit,
	}

	callerARN := middleware.GetCallerARN(ctx)
	operator := extractOperator(callerARN)
	h.recordAudit(ctx, r, accountID, callerARN, operator, http.StatusOK, "", "", "", "", "")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

// parseSince converts a duration shorthand (e.g. "1h", "24h", "7d") or RFC3339 timestamp
// to a nanosecond-precision string matching AuditTimestampFormat for correct DynamoDB comparison.
func parseSince(s string) (string, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(zoa.AuditTimestampFormat), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(zoa.AuditTimestampFormat), nil
	}

	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return "", fmt.Errorf("invalid since value: %s", s)
	}

	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return "", fmt.Errorf("invalid since value: %s", s)
	}

	var d time.Duration
	switch unit {
	case 's':
		d = time.Duration(num) * time.Second
	case 'm':
		d = time.Duration(num) * time.Minute
	case 'h':
		d = time.Duration(num) * time.Hour
	case 'd':
		d = time.Duration(num) * 24 * time.Hour
	default:
		return "", fmt.Errorf("invalid since unit: %c (use s, m, h, or d)", unit)
	}

	return time.Now().UTC().Add(-d).Format(zoa.AuditTimestampFormat), nil
}

// Catalog handles GET /api/v0/trusted-actions
func (h *ZoaHandler) Catalog(w http.ResponseWriter, r *http.Request) {
	templates := h.registry.ListAll()

	items := make([]zoa.TAListItem, 0, len(templates))
	for _, t := range templates {
		items = append(items, zoa.TAListItem{
			Name:        t.Name,
			Scope:       t.Scope,
			Type:        t.Type,
			Description: t.Description,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
		"total": len(items),
	})
}

// Describe handles GET /api/v0/trusted-actions/{action}
func (h *ZoaHandler) Describe(w http.ResponseWriter, r *http.Request) {
	action := mux.Vars(r)["action"]

	tmpl, ok := h.registry.Get(action)
	if !ok {
		h.writeError(w, http.StatusNotFound, "unknown-action", "Trusted action not found: "+action)
		return
	}

	response := &zoa.TADescribeResponse{
		Name:                 tmpl.Name,
		Scope:                tmpl.Scope,
		Type:                 tmpl.Type,
		Description:          tmpl.Description,
		Authorization:        tmpl.Authorization,
		WriteCooldownSeconds: tmpl.WriteCooldownSeconds,
		DryRunAction:         tmpl.DryRunAction,
		Params:               tmpl.Params,
		RequiredFields:       []string{"target_cluster", "jira"},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (h *ZoaHandler) fetchS3Content(ctx context.Context, s3URI string) ([]byte, error) {
	bucket, key := parseS3URI(s3URI)
	if bucket == "" {
		bucket = h.bucketName
		key = s3URI
	}
	result, err := h.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, err
	}
	defer result.Body.Close()
	return io.ReadAll(result.Body)
}

func parseS3URI(uri string) (bucket, key string) {
	if !strings.HasPrefix(uri, "s3://") {
		return "", uri
	}
	path := strings.TrimPrefix(uri, "s3://")
	idx := strings.Index(path, "/")
	if idx < 0 {
		return path, ""
	}
	return path[:idx], path[idx+1:]
}

type includeSelection struct {
	output bool
	logs   bool
}

func parseInclude(raw string) includeSelection {
	if raw == "" {
		return includeSelection{}
	}

	sel := includeSelection{}
	for _, f := range strings.Split(raw, ",") {
		switch strings.TrimSpace(f) {
		case "output":
			sel.output = true
		case "logs":
			sel.logs = true
		}
	}

	return sel
}

func validateParams(tmpl *zoa.TATemplate, params map[string]string) error {
	allowed := make(map[string]bool, len(tmpl.Params))
	for _, p := range tmpl.Params {
		allowed[p.Name] = true
		if p.Required {
			val, ok := params[p.Name]
			if !ok || val == "" {
				return fmt.Errorf("required parameter '%s' is missing", p.Name)
			}
		}
	}

	topLevelFields := map[string]bool{"target_cluster": true, "jira": true, "force": true, "dry_run": true}
	for k := range params {
		if !allowed[k] {
			var msg string
			if len(tmpl.Params) == 0 {
				msg = fmt.Sprintf("unknown parameter '%s'; this action accepts no parameters", k)
			} else {
				names := make([]string, 0, len(tmpl.Params))
				for _, p := range tmpl.Params {
					names = append(names, p.Name)
				}
				msg = fmt.Sprintf("unknown parameter '%s'; allowed parameters: %s", k, strings.Join(names, ", "))
			}
			if topLevelFields[k] {
				msg += fmt.Sprintf(" ('%s' is a top-level request field, not a param)", k)
			}
			return fmt.Errorf("%s", msg)
		}
	}

	if hasParamWithDefault(tmpl, "namespace", "") && hasParamWithDefault(tmpl, "all_namespaces", "false") {
		ns := params["namespace"]
		allNs := params["all_namespaces"]
		if ns == "" && allNs != "true" {
			return fmt.Errorf("specify namespace or set all_namespaces=true")
		}
	}

	return nil
}

func hasParamWithDefault(tmpl *zoa.TATemplate, name, defaultVal string) bool {
	for _, p := range tmpl.Params {
		if p.Name == name && p.Default == defaultVal {
			return true
		}
	}
	return false
}

func isValidJiraFormat(jira string) bool {
	return jiraTicketRegex.MatchString(jira)
}

func extractOperator(callerARN string) string {
	parts := strings.Split(callerARN, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return callerARN
}

func (h *ZoaHandler) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	})
}

func (h *ZoaHandler) checkWriteCooldown(ctx context.Context, accountID, action, targetCluster string, cooldownSeconds int) error {
	since := time.Now().UTC().Add(-time.Duration(cooldownSeconds) * time.Second).Format(time.RFC3339)
	notDryRun := false
	filter := &zoa.ListFilter{
		Action:        action,
		TargetCluster: targetCluster,
		Since:         since,
		DryRun:        &notDryRun,
	}
	recent, err := h.store.List(ctx, accountID, 0, filter)
	if err != nil {
		h.logger.Error("failed to check write cooldown", "error", err)
		return nil
	}
	if len(recent) > 0 {
		return fmt.Errorf("action '%s' was executed on '%s' recently (cooldown: %ds); use force=true to bypass", action, targetCluster, cooldownSeconds)
	}
	return nil
}

func (h *ZoaHandler) checkMaxConcurrent(ctx context.Context, accountID, targetCluster string, maxConcurrent int) error {
	filter := &zoa.ListFilter{
		TargetCluster: targetCluster,
		Status:        string(zoa.StatusRunning),
	}
	running, err := h.store.List(ctx, accountID, int(maxConcurrent+1), filter)
	if err != nil {
		h.logger.Error("failed to check max concurrent", "error", err)
		return nil
	}
	pendingFilter := &zoa.ListFilter{
		TargetCluster: targetCluster,
		Status:        string(zoa.StatusPending),
	}
	pending, err := h.store.List(ctx, accountID, int(maxConcurrent+1), pendingFilter)
	if err != nil {
		h.logger.Error("failed to check max concurrent pending", "error", err)
		return nil
	}
	active := len(running) + len(pending)
	if active >= maxConcurrent {
		return fmt.Errorf("target '%s' has %d active executions (max: %d); wait for some to complete", targetCluster, active, maxConcurrent)
	}
	return nil
}

func (h *ZoaHandler) recordAudit(ctx context.Context, r *http.Request, accountID, callerARN, operator string, statusCode int, action, targetCluster, executionID, jira, approvalState string) {
	if h.auditStore == nil {
		return
	}
	entry := &zoa.AuditEntry{
		AccountID:     accountID,
		CallerARN:     callerARN,
		Operator:      operator,
		Method:        r.Method,
		Path:          r.URL.RequestURI(),
		Action:        action,
		TargetCluster: targetCluster,
		ExecutionID:   executionID,
		Jira:          jira,
		ApprovalState: approvalState,
		StatusCode:    statusCode,
	}
	if err := h.auditStore.Record(ctx, entry); err != nil {
		h.logger.Error("failed to record audit entry", "error", err)
	}
}

// AuditList handles GET /api/v0/trusted-actions/audit
func (h *ZoaHandler) AuditList(w http.ResponseWriter, r *http.Request) {
	if h.auditStore == nil {
		h.writeError(w, http.StatusNotFound, "audit-disabled", "Audit logging is not enabled")
		return
	}

	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	sinceStr := ""
	if s := r.URL.Query().Get("since"); s != "" {
		parsed, err := parseSince(s)
		if err == nil {
			sinceStr = parsed
		}
	}

	filter := &zoa.AuditFilter{
		Action:        r.URL.Query().Get("action"),
		Operator:      r.URL.Query().Get("operator"),
		TargetCluster: r.URL.Query().Get("target"),
		Method:        r.URL.Query().Get("method"),
		ApprovalState: r.URL.Query().Get("approval_state"),
		Since:         sinceStr,
	}

	entries, err := h.auditStore.List(ctx, accountID, limit, filter)
	if err != nil {
		h.logger.Error("failed to list audit entries", "error", err)
		h.writeError(w, http.StatusInternalServerError, "store-error", "Failed to list audit log")
		return
	}

	callerARN := middleware.GetCallerARN(ctx)
	operator := extractOperator(callerARN)
	h.recordAudit(ctx, r, accountID, callerARN, operator, http.StatusOK, "", "", "", "", "")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"kind":  "AuditList",
		"items": entries,
		"total": len(entries),
	})
}


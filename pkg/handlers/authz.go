package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
)

// AuthzHandler handles authorization management endpoints
type AuthzHandler struct {
	checker authz.Checker
	service authz.Service
	logger  *slog.Logger
}

// NewAuthzHandler creates a new AuthzHandler
func NewAuthzHandler(checker authz.Checker, service authz.Service, logger *slog.Logger) *AuthzHandler {
	return &AuthzHandler{
		checker: checker,
		service: service,
		logger:  logger,
	}
}

// Policy request/response types

type CreatePolicyRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Policy      string `json:"policy"` // Native Cedar policy text
}

type PolicyResponse struct {
	Kind        string `json:"kind"`
	PolicyID    string `json:"policyId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

type PolicyListResponse struct {
	Kind  string           `json:"kind"`
	Items []PolicyResponse `json:"items"`
	Total int              `json:"total"`
}

// Group request/response types

type CreateGroupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type GroupResponse struct {
	Kind        string `json:"kind"`
	GroupID     string `json:"groupId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

type GroupListResponse struct {
	Kind  string          `json:"kind"`
	Items []GroupResponse `json:"items"`
	Total int             `json:"total"`
}

type UpdateMembersRequest struct {
	Add    []string `json:"add"`
	Remove []string `json:"remove"`
}

type MemberListResponse struct {
	Kind  string   `json:"kind"`
	Items []string `json:"items"`
	Total int      `json:"total"`
}

// Attachment request/response types

type CreateAttachmentRequest struct {
	PolicyID   string `json:"policyId"`
	TargetType string `json:"targetType"` // "user" or "group"
	TargetID   string `json:"targetId"`   // ARN for user, groupId for group
}

type AttachmentResponse struct {
	Kind         string `json:"kind"`
	AttachmentID string `json:"attachmentId"`
	PolicyID     string `json:"policyId"`
	TargetType   string `json:"targetType"`
	TargetID     string `json:"targetId"`
	CreatedAt    string `json:"createdAt"`
}

type AttachmentListResponse struct {
	Kind  string               `json:"kind"`
	Items []AttachmentResponse `json:"items"`
	Total int                  `json:"total"`
}

// Admin request/response types

type AddAdminRequest struct {
	PrincipalARN string `json:"principalArn"`
}

// Authorization check request/response types

type CheckAuthorizationRequest struct {
	Principal    string            `json:"principal"`    // Principal ARN making the request
	Action       string            `json:"action"`       // Action being performed (e.g., "rosa:CreateCluster")
	Resource     string            `json:"resource"`     // Resource ARN (e.g., "arn:aws:rosa:us-west-2:123456789012:cluster/*")
	Context      map[string]any    `json:"context"`      // Additional context (e.g., request tags)
	ResourceTags map[string]string `json:"resourceTags"` // Tags on the resource
}

type CheckAuthorizationResponse struct {
	Kind     string `json:"kind"`
	Decision string `json:"decision"` // "ALLOW" or "DENY"
	Reason   string `json:"reason,omitempty"`
}

type AdminListResponse struct {
	Kind  string   `json:"kind"`
	Items []string `json:"items"`
	Total int      `json:"total"`
}

// Policy Handlers

func (h *AuthzHandler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	var req CreatePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
		return
	}

	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "missing-name", "name is required")
		return
	}

	if req.Policy == "" {
		h.writeError(w, http.StatusBadRequest, "missing-policy", "policy (Cedar text) is required")
		return
	}

	p, err := h.service.CreatePolicy(ctx, accountID, req.Name, req.Description, req.Policy)
	if err != nil {
		h.logger.Error("failed to create policy", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusBadRequest, "invalid-policy", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(PolicyResponse{
		Kind:        "Policy",
		PolicyID:    p.PolicyID,
		Name:        p.Name,
		Description: p.Description,
		CreatedAt:   p.CreatedAt,
	})
}

func (h *AuthzHandler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	policies, err := h.service.ListPolicies(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to list policies", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to list policies")
		return
	}

	items := make([]PolicyResponse, len(policies))
	for i, p := range policies {
		items[i] = PolicyResponse{
			Kind:        "Policy",
			PolicyID:    p.PolicyID,
			Name:        p.Name,
			Description: p.Description,
			CreatedAt:   p.CreatedAt,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(PolicyListResponse{
		Kind:  "PolicyList",
		Items: items,
		Total: len(items),
	})
}

func (h *AuthzHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	policyID := vars["id"]

	p, err := h.service.GetPolicy(ctx, accountID, policyID)
	if err != nil {
		h.logger.Error("failed to get policy", "error", err, "account_id", accountID, "policy_id", policyID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to get policy")
		return
	}

	if p == nil {
		h.writeError(w, http.StatusNotFound, "not-found", "Policy not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(PolicyResponse{
		Kind:        "Policy",
		PolicyID:    p.PolicyID,
		Name:        p.Name,
		Description: p.Description,
		CreatedAt:   p.CreatedAt,
	})
}

func (h *AuthzHandler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	policyID := vars["id"]

	var req CreatePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
		return
	}

	p, err := h.service.UpdatePolicy(ctx, accountID, policyID, req.Name, req.Description, req.Policy)
	if err != nil {
		h.logger.Error("failed to update policy", "error", err, "account_id", accountID, "policy_id", policyID)
		h.writeError(w, http.StatusBadRequest, "invalid-policy", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(PolicyResponse{
		Kind:        "Policy",
		PolicyID:    p.PolicyID,
		Name:        p.Name,
		Description: p.Description,
		CreatedAt:   p.CreatedAt,
	})
}

func (h *AuthzHandler) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	policyID := vars["id"]

	err := h.service.DeletePolicy(ctx, accountID, policyID)
	if err != nil {
		h.logger.Error("failed to delete policy", "error", err, "account_id", accountID, "policy_id", policyID)
		if err.Error() == "cannot delete policy with existing attachments" {
			h.writeError(w, http.StatusConflict, "policy-in-use", err.Error())
			return
		}
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to delete policy")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Group Handlers

func (h *AuthzHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	var req CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
		return
	}

	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "missing-name", "name is required")
		return
	}

	g, err := h.service.CreateGroup(ctx, accountID, req.Name, req.Description)
	if err != nil {
		h.logger.Error("failed to create group", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to create group")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(GroupResponse{
		Kind:        "Group",
		GroupID:     g.GroupID,
		Name:        g.Name,
		Description: g.Description,
		CreatedAt:   g.CreatedAt,
	})
}

func (h *AuthzHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	groups, err := h.service.ListGroups(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to list groups", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to list groups")
		return
	}

	items := make([]GroupResponse, len(groups))
	for i, g := range groups {
		items[i] = GroupResponse{
			Kind:        "Group",
			GroupID:     g.GroupID,
			Name:        g.Name,
			Description: g.Description,
			CreatedAt:   g.CreatedAt,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GroupListResponse{
		Kind:  "GroupList",
		Items: items,
		Total: len(items),
	})
}

func (h *AuthzHandler) GetGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	groupID := vars["id"]

	g, err := h.service.GetGroup(ctx, accountID, groupID)
	if err != nil {
		h.logger.Error("failed to get group", "error", err, "account_id", accountID, "group_id", groupID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to get group")
		return
	}

	if g == nil {
		h.writeError(w, http.StatusNotFound, "not-found", "Group not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GroupResponse{
		Kind:        "Group",
		GroupID:     g.GroupID,
		Name:        g.Name,
		Description: g.Description,
		CreatedAt:   g.CreatedAt,
	})
}

func (h *AuthzHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	groupID := vars["id"]

	err := h.service.DeleteGroup(ctx, accountID, groupID)
	if err != nil {
		h.logger.Error("failed to delete group", "error", err, "account_id", accountID, "group_id", groupID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to delete group")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthzHandler) UpdateGroupMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	groupID := vars["id"]

	var req UpdateMembersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
		return
	}

	// Add members
	for _, memberARN := range req.Add {
		if err := h.service.AddGroupMember(ctx, accountID, groupID, memberARN); err != nil {
			h.logger.Error("failed to add group member", "error", err, "account_id", accountID, "group_id", groupID, "member", memberARN)
			h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to add group member")
			return
		}
	}

	// Remove members
	for _, memberARN := range req.Remove {
		if err := h.service.RemoveGroupMember(ctx, accountID, groupID, memberARN); err != nil {
			h.logger.Error("failed to remove group member", "error", err, "account_id", accountID, "group_id", groupID, "member", memberARN)
			h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to remove group member")
			return
		}
	}

	// Return updated member list
	members, err := h.service.ListGroupMembers(ctx, accountID, groupID)
	if err != nil {
		h.logger.Error("failed to list group members", "error", err, "account_id", accountID, "group_id", groupID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to list group members")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MemberListResponse{
		Kind:  "MemberList",
		Items: members,
		Total: len(members),
	})
}

func (h *AuthzHandler) ListGroupMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	groupID := vars["id"]

	members, err := h.service.ListGroupMembers(ctx, accountID, groupID)
	if err != nil {
		h.logger.Error("failed to list group members", "error", err, "account_id", accountID, "group_id", groupID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to list group members")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MemberListResponse{
		Kind:  "MemberList",
		Items: members,
		Total: len(members),
	})
}

// Attachment Handlers

func (h *AuthzHandler) CreateAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	var req CreateAttachmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
		return
	}

	if req.PolicyID == "" || req.TargetType == "" || req.TargetID == "" {
		h.writeError(w, http.StatusBadRequest, "missing-fields", "policyId, targetType, and targetId are required")
		return
	}

	if req.TargetType != "user" && req.TargetType != "group" {
		h.writeError(w, http.StatusBadRequest, "invalid-target-type", "targetType must be 'user' or 'group'")
		return
	}

	a, err := h.service.AttachPolicy(ctx, accountID, req.PolicyID, authz.TargetType(req.TargetType), req.TargetID)
	if err != nil {
		h.logger.Error("failed to attach policy", "error", err, "account_id", accountID, "policy_id", req.PolicyID)
		h.writeError(w, http.StatusBadRequest, "attachment-failed", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(AttachmentResponse{
		Kind:         "Attachment",
		AttachmentID: a.AttachmentID,
		PolicyID:     a.PolicyID,
		TargetType:   string(a.TargetType),
		TargetID:     a.TargetID,
		CreatedAt:    a.CreatedAt,
	})
}

func (h *AuthzHandler) ListAttachments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	// Parse filter parameters
	filter := authz.AttachmentFilter{
		PolicyID:   r.URL.Query().Get("policyId"),
		TargetType: authz.TargetType(r.URL.Query().Get("targetType")),
		TargetID:   r.URL.Query().Get("targetId"),
	}

	attachments, err := h.service.ListAttachments(ctx, accountID, filter)
	if err != nil {
		h.logger.Error("failed to list attachments", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to list attachments")
		return
	}

	items := make([]AttachmentResponse, len(attachments))
	for i, a := range attachments {
		items[i] = AttachmentResponse{
			Kind:         "Attachment",
			AttachmentID: a.AttachmentID,
			PolicyID:     a.PolicyID,
			TargetType:   string(a.TargetType),
			TargetID:     a.TargetID,
			CreatedAt:    a.CreatedAt,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AttachmentListResponse{
		Kind:  "AttachmentList",
		Items: items,
		Total: len(items),
	})
}

func (h *AuthzHandler) DeleteAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	attachmentID := vars["id"]

	err := h.service.DetachPolicy(ctx, accountID, attachmentID)
	if err != nil {
		h.logger.Error("failed to detach policy", "error", err, "account_id", accountID, "attachment_id", attachmentID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to detach policy")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Admin Handlers

func (h *AuthzHandler) AddAdmin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	callerARN := middleware.GetCallerARN(ctx)

	var req AddAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
		return
	}

	if req.PrincipalARN == "" {
		h.writeError(w, http.StatusBadRequest, "missing-principal-arn", "principalArn is required")
		return
	}

	err := h.service.AddAdmin(ctx, accountID, req.PrincipalARN, callerARN)
	if err != nil {
		h.logger.Error("failed to add admin", "error", err, "account_id", accountID, "principal_arn", req.PrincipalARN)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to add admin")
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"kind":         "Admin",
		"principalArn": req.PrincipalARN,
	})
}

func (h *AuthzHandler) ListAdmins(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	admins, err := h.service.ListAdmins(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to list admins", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to list admins")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AdminListResponse{
		Kind:  "AdminList",
		Items: admins,
		Total: len(admins),
	})
}

func (h *AuthzHandler) RemoveAdmin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	// The ARN is URL-encoded in the path
	principalARN := vars["arn"]

	err := h.service.RemoveAdmin(ctx, accountID, principalARN)
	if err != nil {
		h.logger.Error("failed to remove admin", "error", err, "account_id", accountID, "principal_arn", principalARN)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to remove admin")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// CheckAuthorization evaluates an authorization request and returns the decision.
func (h *AuthzHandler) CheckAuthorization(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	var req CheckAuthorizationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
		return
	}

	if req.Principal == "" {
		h.writeError(w, http.StatusBadRequest, "missing-principal", "principal is required")
		return
	}

	if req.Action == "" {
		h.writeError(w, http.StatusBadRequest, "missing-action", "action is required")
		return
	}

	if req.Resource == "" {
		h.writeError(w, http.StatusBadRequest, "missing-resource", "resource is required")
		return
	}

	// Build authorization request
	authzReq := &authz.AuthzRequest{
		AccountID:    accountID,
		CallerARN:    req.Principal,
		Action:       req.Action,
		Resource:     req.Resource,
		ResourceTags: req.ResourceTags,
		Context:      req.Context,
	}

	// Check authorization
	allowed, err := h.checker.Authorize(ctx, authzReq)
	if err != nil {
		h.logger.Error("authorization check failed", "error", err, "account_id", accountID, "principal", req.Principal, "action", req.Action)
		h.writeError(w, http.StatusInternalServerError, "authorization-error", err.Error())
		return
	}

	decision := "DENY"
	if allowed {
		decision = "ALLOW"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CheckAuthorizationResponse{
		Kind:     "AuthorizationDecision",
		Decision: decision,
	})
}

func (h *AuthzHandler) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]any{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

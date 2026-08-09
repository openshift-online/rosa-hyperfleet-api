package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz/store"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

type mockAuthzService struct {
	enableAccountFn func(ctx context.Context, accountID, createdBy string, isPrivileged bool) (*store.Account, error)
	getAccountFn    func(ctx context.Context, accountID string) (*store.Account, error)
	addAdminFn      func(ctx context.Context, accountID, principalARN, createdBy string) error
}

func (m *mockAuthzService) EnableAccount(ctx context.Context, accountID, createdBy string, isPrivileged bool) (*store.Account, error) {
	if m.enableAccountFn != nil {
		return m.enableAccountFn(ctx, accountID, createdBy, isPrivileged)
	}
	return &store.Account{AccountID: accountID, Privileged: isPrivileged, CreatedBy: createdBy}, nil
}

func (m *mockAuthzService) GetAccount(ctx context.Context, accountID string) (*store.Account, error) {
	if m.getAccountFn != nil {
		return m.getAccountFn(ctx, accountID)
	}
	return nil, nil
}

func (m *mockAuthzService) AddAdmin(ctx context.Context, accountID, principalARN, createdBy string) error {
	if m.addAdminFn != nil {
		return m.addAdminFn(ctx, accountID, principalARN, createdBy)
	}
	return nil
}

func (m *mockAuthzService) DisableAccount(ctx context.Context, accountID string) error { return nil }
func (m *mockAuthzService) ListAccounts(ctx context.Context) ([]*store.Account, error) {
	return nil, nil
}
func (m *mockAuthzService) RemoveAdmin(ctx context.Context, accountID, principalARN string) error {
	return nil
}
func (m *mockAuthzService) ListAdmins(ctx context.Context, accountID string) ([]string, error) {
	return nil, nil
}
func (m *mockAuthzService) CreateGroup(ctx context.Context, accountID, name, description string) (*store.Group, error) {
	return nil, nil
}
func (m *mockAuthzService) GetGroup(ctx context.Context, accountID, groupID string) (*store.Group, error) {
	return nil, nil
}
func (m *mockAuthzService) DeleteGroup(ctx context.Context, accountID, groupID string) error {
	return nil
}
func (m *mockAuthzService) ListGroups(ctx context.Context, accountID string) ([]*store.Group, error) {
	return nil, nil
}
func (m *mockAuthzService) AddGroupMember(ctx context.Context, accountID, groupID, memberARN string) error {
	return nil
}
func (m *mockAuthzService) RemoveGroupMember(ctx context.Context, accountID, groupID, memberARN string) error {
	return nil
}
func (m *mockAuthzService) ListGroupMembers(ctx context.Context, accountID, groupID string) ([]string, error) {
	return nil, nil
}
func (m *mockAuthzService) GetUserGroups(ctx context.Context, accountID, memberARN string) ([]string, error) {
	return nil, nil
}
func (m *mockAuthzService) CreatePolicy(ctx context.Context, accountID, name, description, cedarPolicy string) (*store.Policy, error) {
	return nil, nil
}
func (m *mockAuthzService) GetPolicy(ctx context.Context, accountID, policyID string) (*store.Policy, error) {
	return nil, nil
}
func (m *mockAuthzService) UpdatePolicy(ctx context.Context, accountID, policyID, name, description, cedarPolicy string) (*store.Policy, error) {
	return nil, nil
}
func (m *mockAuthzService) DeletePolicy(ctx context.Context, accountID, policyID string) error {
	return nil
}
func (m *mockAuthzService) ListPolicies(ctx context.Context, accountID string) ([]*store.Policy, error) {
	return nil, nil
}
func (m *mockAuthzService) AttachPolicy(ctx context.Context, accountID, policyID string, targetType authz.TargetType, targetID string) (*authz.Attachment, error) {
	return nil, nil
}
func (m *mockAuthzService) DetachPolicy(ctx context.Context, accountID, attachmentID string) error {
	return nil
}
func (m *mockAuthzService) ListAttachments(ctx context.Context, accountID string, filter authz.AttachmentFilter) ([]*authz.Attachment, error) {
	return nil, nil
}

var _ authz.Service = (*mockAuthzService)(nil)

func newAccountsHandler(mock *mockAuthzService) *AccountsHandler {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	return NewAccountsHandler(mock, logger)
}

func accountRequest(body any) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/accounts", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyCallerARN, "arn:aws:iam::599476212575:role/privileged-role")
	return req.WithContext(ctx)
}

func TestAccounts_Create_NonPrivilegedRequiresAdminArn(t *testing.T) {
	h := newAccountsHandler(&mockAuthzService{})

	req := accountRequest(EnableAccountRequest{
		AccountID:  "754250776154",
		Privileged: false,
	})
	w := httptest.NewRecorder()
	h.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["code"] != "ACCOUNTS-MGMT-CREATE-003" {
		t.Errorf("expected code=ACCOUNTS-MGMT-CREATE-003 (missing-admin-arn), got %v", resp["code"])
	}
}

func TestAccounts_Create_PrivilegedDoesNotRequireAdminArn(t *testing.T) {
	h := newAccountsHandler(&mockAuthzService{})

	req := accountRequest(EnableAccountRequest{
		AccountID:  "599476212575",
		Privileged: true,
	})
	w := httptest.NewRecorder()
	h.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", w.Code)
	}
}

func TestAccounts_Create_NonPrivilegedWithAdminArn(t *testing.T) {
	var addedAdmin string
	mock := &mockAuthzService{
		addAdminFn: func(ctx context.Context, accountID, principalARN, createdBy string) error {
			addedAdmin = principalARN
			return nil
		},
	}
	h := newAccountsHandler(mock)

	req := accountRequest(EnableAccountRequest{
		AccountID:  "754250776154",
		Privileged: false,
		AdminArn:   "arn:aws:iam::754250776154:role/admin-role",
	})
	w := httptest.NewRecorder()
	h.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", w.Code)
	}
	if addedAdmin != "arn:aws:iam::754250776154:role/admin-role" {
		t.Errorf("expected AddAdmin called with admin-role, got %q", addedAdmin)
	}
}

func TestAccounts_Create_PrivilegedIgnoresAdminArn(t *testing.T) {
	addAdminCalled := false
	mock := &mockAuthzService{
		addAdminFn: func(ctx context.Context, accountID, principalARN, createdBy string) error {
			addAdminCalled = true
			return nil
		},
	}
	h := newAccountsHandler(mock)

	req := accountRequest(EnableAccountRequest{
		AccountID:  "599476212575",
		Privileged: true,
		AdminArn:   "arn:aws:iam::599476212575:role/some-role",
	})
	w := httptest.NewRecorder()
	h.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", w.Code)
	}
	if addAdminCalled {
		t.Error("expected AddAdmin NOT to be called for privileged accounts")
	}
}

func TestAccounts_Create_MissingAccountID(t *testing.T) {
	h := newAccountsHandler(&mockAuthzService{})

	req := accountRequest(EnableAccountRequest{})
	w := httptest.NewRecorder()
	h.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["code"] != "ACCOUNTS-MGMT-CREATE-002" {
		t.Errorf("expected code=ACCOUNTS-MGMT-CREATE-002 (missing-account-id), got %v", resp["code"])
	}
}

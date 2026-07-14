package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/verifiedpermissions"
	avptypes "github.com/aws/aws-sdk-go-v2/service/verifiedpermissions/types"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz/client"
	"github.com/openshift/rosa-hyperfleet-api/pkg/authz/privileged"
	"github.com/openshift/rosa-hyperfleet-api/pkg/authz/schema"
	"github.com/openshift/rosa-hyperfleet-api/pkg/authz/store"
)

// AuthzRequest represents an authorization request
type AuthzRequest struct {
	AccountID    string
	CallerARN    string
	Action       string
	Resource     string
	ResourceTags map[string]string
	RequestTags  map[string]string
	Context      map[string]any
}

// Checker handles authorization decisions (used by middleware)
type Checker interface {
	Authorize(ctx context.Context, req *AuthzRequest) (bool, error)
	IsPrivileged(ctx context.Context, accountID string) (bool, error)
	IsAdmin(ctx context.Context, accountID, principalARN string) (bool, error)
	IsAccountProvisioned(ctx context.Context, accountID string) (bool, error)
}

// TargetType represents the type of attachment target
type TargetType string

const (
	TargetTypeUser  TargetType = "user"
	TargetTypeGroup TargetType = "group"
)

// Attachment represents a policy attachment (backed by an AVP template-linked policy)
type Attachment struct {
	AttachmentID string     `json:"attachmentId"` // = AVP policy ID
	PolicyID     string     `json:"policyId"`     // = AVP template ID
	TargetType   TargetType `json:"targetType"`
	TargetID     string     `json:"targetId"`
	CreatedAt    string     `json:"createdAt"`
}

// AttachmentFilter defines filter options for listing attachments
type AttachmentFilter struct {
	PolicyID   string
	TargetType TargetType
	TargetID   string
}

// Service manages authz resources (used by handlers)
type Service interface {
	// Account lifecycle
	EnableAccount(ctx context.Context, accountID, createdBy string, isPrivileged bool) (*store.Account, error)
	DisableAccount(ctx context.Context, accountID string) error
	GetAccount(ctx context.Context, accountID string) (*store.Account, error)
	ListAccounts(ctx context.Context) ([]*store.Account, error)

	// Admin management
	AddAdmin(ctx context.Context, accountID, principalARN, createdBy string) error
	RemoveAdmin(ctx context.Context, accountID, principalARN string) error
	ListAdmins(ctx context.Context, accountID string) ([]string, error)

	// Group management
	CreateGroup(ctx context.Context, accountID, name, description string) (*store.Group, error)
	GetGroup(ctx context.Context, accountID, groupID string) (*store.Group, error)
	DeleteGroup(ctx context.Context, accountID, groupID string) error
	ListGroups(ctx context.Context, accountID string) ([]*store.Group, error)
	AddGroupMember(ctx context.Context, accountID, groupID, memberARN string) error
	RemoveGroupMember(ctx context.Context, accountID, groupID, memberARN string) error
	ListGroupMembers(ctx context.Context, accountID, groupID string) ([]string, error)
	GetUserGroups(ctx context.Context, accountID, memberARN string) ([]string, error)

	// Policy management — policy templates stored in AVP
	CreatePolicy(ctx context.Context, accountID, name, description, cedarPolicy string) (*store.Policy, error)
	GetPolicy(ctx context.Context, accountID, policyID string) (*store.Policy, error)
	UpdatePolicy(ctx context.Context, accountID, policyID, name, description, cedarPolicy string) (*store.Policy, error)
	DeletePolicy(ctx context.Context, accountID, policyID string) error
	ListPolicies(ctx context.Context, accountID string) ([]*store.Policy, error)

	// Attachment management
	AttachPolicy(ctx context.Context, accountID, policyID string, targetType TargetType, targetID string) (*Attachment, error)
	DetachPolicy(ctx context.Context, accountID, attachmentID string) error
	ListAttachments(ctx context.Context, accountID string, filter AttachmentFilter) ([]*Attachment, error)
}

// authorizerImpl implements both Checker and Service interfaces
type authorizerImpl struct {
	cfg             *Config
	logger          *slog.Logger
	avpClient       client.AVPClient
	privilegedCheck *privileged.Checker
	accountStore    *store.AccountStore
	adminStore      *store.AdminStore
	groupStore      *store.GroupStore
	memberStore     *store.MemberStore
}

// New creates a new authorizer that implements both Checker and Service
func New(cfg *Config, dynamoClient client.DynamoDBClient, avpClient client.AVPClient, logger *slog.Logger) *authorizerImpl {
	privilegedChecker := privileged.NewChecker(
		cfg.AccountsTableName,
		dynamoClient,
		logger,
	)

	return &authorizerImpl{
		cfg:             cfg,
		logger:          logger,
		avpClient:       avpClient,
		privilegedCheck: privilegedChecker,
		accountStore:    store.NewAccountStore(cfg.AccountsTableName, dynamoClient, logger),
		adminStore:      store.NewAdminStore(cfg.AdminsTableName, dynamoClient, logger),
		groupStore:      store.NewGroupStore(cfg.GroupsTableName, dynamoClient, logger),
		memberStore:     store.NewMemberStore(cfg.MembersTableName, dynamoClient, logger),
	}
}

// Authorize performs the authorization check
func (a *authorizerImpl) Authorize(ctx context.Context, req *AuthzRequest) (bool, error) {
	// Check if privileged (bypass all)
	isPriv, err := a.IsPrivileged(ctx, req.AccountID)
	if err != nil {
		a.logger.Error("failed to check privileged status", "error", err, "account_id", req.AccountID)
		return false, err
	}
	if isPriv {
		a.logger.Debug("privileged account bypass", "account_id", req.AccountID)
		return true, nil
	}

	// Check if account is provisioned
	account, err := a.accountStore.Get(ctx, req.AccountID)
	if err != nil {
		return false, fmt.Errorf("failed to get account: %w", err)
	}
	if account == nil {
		a.logger.Warn("account not provisioned", "account_id", req.AccountID)
		return false, fmt.Errorf("account not provisioned: %s", req.AccountID)
	}

	// Check if caller is admin (bypass Cedar)
	isAdm, err := a.IsAdmin(ctx, req.AccountID, req.CallerARN)
	if err != nil {
		return false, err
	}
	if isAdm {
		a.logger.Debug("admin bypass", "account_id", req.AccountID, "caller_arn", req.CallerARN)
		return true, nil
	}

	// Get user's group memberships
	groups, err := a.memberStore.GetUserGroups(ctx, req.AccountID, req.CallerARN)
	if err != nil {
		return false, fmt.Errorf("failed to get user groups: %w", err)
	}

	// Build AVP request
	avpReq := a.buildAVPRequest(req, groups, account.PolicyStoreID)

	// Call AVP
	resp, err := a.avpClient.IsAuthorized(ctx, avpReq)
	if err != nil {
		a.logger.Error("AVP authorization failed", "error", err, "account_id", req.AccountID)
		return false, fmt.Errorf("authorization check failed: %w", err)
	}

	decision := resp.Decision == avptypes.DecisionAllow
	a.logger.Info("authorization decision",
		"account_id", req.AccountID,
		"caller_arn", req.CallerARN,
		"action", req.Action,
		"resource", req.Resource,
		"decision", decision,
	)

	return decision, nil
}

// buildAVPRequest creates the AVP IsAuthorized request
func (a *authorizerImpl) buildAVPRequest(req *AuthzRequest, groups []string, policyStoreID string) *verifiedpermissions.IsAuthorizedInput {
	// Build principal
	principal := &avptypes.EntityIdentifier{
		EntityType: aws.String("ROSA::Principal"),
		EntityId:   aws.String(req.CallerARN),
	}

	// Build action
	action := &avptypes.ActionIdentifier{
		ActionType: aws.String("ROSA::Action"),
		ActionId:   aws.String(req.Action),
	}

	// Build resource
	resource := &avptypes.EntityIdentifier{
		EntityType: aws.String("ROSA::Resource"),
		EntityId:   aws.String(req.Resource),
	}

	// Build context
	contextMap := make(map[string]avptypes.AttributeValue)

	// Add principal info to context
	contextMap["principalArn"] = &avptypes.AttributeValueMemberString{Value: req.CallerARN}
	contextMap["principalAccount"] = &avptypes.AttributeValueMemberString{Value: req.AccountID}

	// Add request tags to context
	if len(req.RequestTags) > 0 {
		requestTagsMap := make(map[string]avptypes.AttributeValue)
		for k, v := range req.RequestTags {
			requestTagsMap[k] = &avptypes.AttributeValueMemberString{Value: v}
		}
		contextMap["requestTags"] = &avptypes.AttributeValueMemberRecord{Value: requestTagsMap}
	}

	// Add tag keys to context
	if len(req.RequestTags) > 0 {
		var tagKeys []avptypes.AttributeValue
		for k := range req.RequestTags {
			tagKeys = append(tagKeys, &avptypes.AttributeValueMemberString{Value: k})
		}
		contextMap["tagKeys"] = &avptypes.AttributeValueMemberSet{Value: tagKeys}
	}

	// Add custom context
	for k, v := range req.Context {
		if av := toAttributeValue(v); av != nil {
			contextMap[k] = av
		}
	}

	// Build entities (for group membership)
	var entities []avptypes.EntityItem
	entities = append(entities, avptypes.EntityItem{
		Identifier: principal,
	})

	// Add group memberships
	for _, groupID := range groups {
		entities = append(entities, avptypes.EntityItem{
			Identifier: &avptypes.EntityIdentifier{
				EntityType: aws.String("ROSA::Group"),
				EntityId:   aws.String(groupID),
			},
		})
	}

	// Add resource with tags
	if len(req.ResourceTags) > 0 {
		tagsMap := make(map[string]avptypes.AttributeValue)
		for k, v := range req.ResourceTags {
			tagsMap[k] = &avptypes.AttributeValueMemberString{Value: v}
		}
		entities = append(entities, avptypes.EntityItem{
			Identifier: resource,
			Attributes: map[string]avptypes.AttributeValue{
				"tags": &avptypes.AttributeValueMemberRecord{Value: tagsMap},
			},
		})
	}

	return &verifiedpermissions.IsAuthorizedInput{
		PolicyStoreId: aws.String(policyStoreID),
		Principal:     principal,
		Action:        action,
		Resource:      resource,
		Context: &avptypes.ContextDefinitionMemberContextMap{
			Value: contextMap,
		},
		Entities: &avptypes.EntitiesDefinitionMemberEntityList{
			Value: entities,
		},
	}
}

// toAttributeValue converts a Go value (from JSON unmarshalling) to an AVP AttributeValue.
func toAttributeValue(v any) avptypes.AttributeValue {
	switch val := v.(type) {
	case string:
		return &avptypes.AttributeValueMemberString{Value: val}
	case bool:
		return &avptypes.AttributeValueMemberBoolean{Value: val}
	case float64:
		return &avptypes.AttributeValueMemberLong{Value: int64(val)}
	case map[string]any:
		record := make(map[string]avptypes.AttributeValue, len(val))
		for k, item := range val {
			if av := toAttributeValue(item); av != nil {
				record[k] = av
			}
		}
		return &avptypes.AttributeValueMemberRecord{Value: record}
	case []any:
		set := make([]avptypes.AttributeValue, 0, len(val))
		for _, item := range val {
			if av := toAttributeValue(item); av != nil {
				set = append(set, av)
			}
		}
		return &avptypes.AttributeValueMemberSet{Value: set}
	default:
		return nil
	}
}

// IsPrivileged checks if an account is privileged
func (a *authorizerImpl) IsPrivileged(ctx context.Context, accountID string) (bool, error) {
	return a.privilegedCheck.IsPrivileged(ctx, accountID)
}

// EnableAccount creates a new account with an optional policy store
func (a *authorizerImpl) EnableAccount(ctx context.Context, accountID, createdBy string, isPrivileged bool) (*store.Account, error) {
	account := &store.Account{
		AccountID:  accountID,
		Privileged: isPrivileged,
		CreatedBy:  createdBy,
	}

	// If not privileged, create a policy store
	if !isPrivileged {
		psResp, err := a.avpClient.CreatePolicyStore(ctx, &verifiedpermissions.CreatePolicyStoreInput{
			ValidationSettings: &avptypes.ValidationSettings{
				Mode: avptypes.ValidationModeStrict,
			},
			Description: aws.String(fmt.Sprintf("ROSA authorization policy store for account %s", accountID)),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create policy store: %w", err)
		}

		account.PolicyStoreID = *psResp.PolicyStoreId

		// Set up the schema
		_, err = a.avpClient.PutSchema(ctx, &verifiedpermissions.PutSchemaInput{
			PolicyStoreId: psResp.PolicyStoreId,
			Definition: &avptypes.SchemaDefinitionMemberCedarJson{
				Value: schema.CedarSchemaJSON,
			},
		})
		if err != nil {
			// Try to clean up the policy store
			_, _ = a.avpClient.DeletePolicyStore(ctx, &verifiedpermissions.DeletePolicyStoreInput{
				PolicyStoreId: psResp.PolicyStoreId,
			})
			return nil, fmt.Errorf("failed to set policy store schema: %w", err)
		}
	}

	if err := a.accountStore.Create(ctx, account); err != nil {
		// Clean up policy store if we created one
		if account.PolicyStoreID != "" {
			_, _ = a.avpClient.DeletePolicyStore(ctx, &verifiedpermissions.DeletePolicyStoreInput{
				PolicyStoreId: aws.String(account.PolicyStoreID),
			})
		}
		return nil, err
	}

	a.logger.Info("account enabled", "account_id", accountID, "privileged", isPrivileged)
	return account, nil
}

// DisableAccount removes an account and its policy store
func (a *authorizerImpl) DisableAccount(ctx context.Context, accountID string) error {
	account, err := a.accountStore.Get(ctx, accountID)
	if err != nil {
		return err
	}
	if account == nil {
		return fmt.Errorf("account not found: %s", accountID)
	}

	// Delete policy store if exists
	if account.PolicyStoreID != "" {
		_, err = a.avpClient.DeletePolicyStore(ctx, &verifiedpermissions.DeletePolicyStoreInput{
			PolicyStoreId: aws.String(account.PolicyStoreID),
		})
		if err != nil {
			a.logger.Warn("failed to delete policy store", "error", err, "policy_store_id", account.PolicyStoreID)
		}
	}

	return a.accountStore.Delete(ctx, accountID)
}

// GetAccount retrieves an account
func (a *authorizerImpl) GetAccount(ctx context.Context, accountID string) (*store.Account, error) {
	return a.accountStore.Get(ctx, accountID)
}

// ListAccounts returns all accounts
func (a *authorizerImpl) ListAccounts(ctx context.Context) ([]*store.Account, error) {
	return a.accountStore.List(ctx)
}

// IsAccountProvisioned checks if an account is provisioned
func (a *authorizerImpl) IsAccountProvisioned(ctx context.Context, accountID string) (bool, error) {
	// Privileged accounts are always considered provisioned
	isPriv, err := a.IsPrivileged(ctx, accountID)
	if err != nil {
		return false, err
	}
	if isPriv {
		return true, nil
	}

	return a.accountStore.Exists(ctx, accountID)
}

// IsAdmin checks if a principal is an admin
func (a *authorizerImpl) IsAdmin(ctx context.Context, accountID, principalARN string) (bool, error) {
	return a.adminStore.IsAdmin(ctx, accountID, principalARN)
}

// AddAdmin adds an admin
func (a *authorizerImpl) AddAdmin(ctx context.Context, accountID, principalARN, createdBy string) error {
	admin := &store.Admin{
		AccountID:    accountID,
		PrincipalARN: principalARN,
		CreatedBy:    createdBy,
	}
	return a.adminStore.Add(ctx, admin)
}

// RemoveAdmin removes an admin
func (a *authorizerImpl) RemoveAdmin(ctx context.Context, accountID, principalARN string) error {
	return a.adminStore.Remove(ctx, accountID, principalARN)
}

// ListAdmins returns all admin ARNs for an account
func (a *authorizerImpl) ListAdmins(ctx context.Context, accountID string) ([]string, error) {
	return a.adminStore.ListARNs(ctx, accountID)
}

// CreateGroup creates a new group
func (a *authorizerImpl) CreateGroup(ctx context.Context, accountID, name, description string) (*store.Group, error) {
	return a.groupStore.Create(ctx, accountID, name, description)
}

// GetGroup retrieves a group
func (a *authorizerImpl) GetGroup(ctx context.Context, accountID, groupID string) (*store.Group, error) {
	return a.groupStore.Get(ctx, accountID, groupID)
}

// DeleteGroup removes a group and its members
func (a *authorizerImpl) DeleteGroup(ctx context.Context, accountID, groupID string) error {
	// First remove all members
	if err := a.memberStore.RemoveAllGroupMembers(ctx, accountID, groupID); err != nil {
		return err
	}

	// Then delete the group
	return a.groupStore.Delete(ctx, accountID, groupID)
}

// ListGroups returns all groups for an account
func (a *authorizerImpl) ListGroups(ctx context.Context, accountID string) ([]*store.Group, error) {
	return a.groupStore.List(ctx, accountID)
}

// AddGroupMember adds a member to a group
func (a *authorizerImpl) AddGroupMember(ctx context.Context, accountID, groupID, memberARN string) error {
	return a.memberStore.Add(ctx, accountID, groupID, memberARN)
}

// RemoveGroupMember removes a member from a group
func (a *authorizerImpl) RemoveGroupMember(ctx context.Context, accountID, groupID, memberARN string) error {
	return a.memberStore.Remove(ctx, accountID, groupID, memberARN)
}

// ListGroupMembers returns all members of a group
func (a *authorizerImpl) ListGroupMembers(ctx context.Context, accountID, groupID string) ([]string, error) {
	return a.memberStore.ListGroupMembers(ctx, accountID, groupID)
}

// GetUserGroups returns all groups a user belongs to
func (a *authorizerImpl) GetUserGroups(ctx context.Context, accountID, memberARN string) ([]string, error) {
	return a.memberStore.GetUserGroups(ctx, accountID, memberARN)
}

// policyMeta encodes policy name and description into AVP's template Description field.
type policyMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func encodePolicyMeta(name, description string) string {
	b, _ := json.Marshal(policyMeta{Name: name, Description: description})
	return string(b)
}

func decodePolicyMeta(encoded string) (name, description string) {
	var meta policyMeta
	if err := json.Unmarshal([]byte(encoded), &meta); err != nil {
		return encoded, ""
	}
	return meta.Name, meta.Description
}

// getAccountPolicyStoreID returns the policy store ID for an account, or an error if the account
// is not found or has no policy store.
func (a *authorizerImpl) getAccountPolicyStoreID(ctx context.Context, accountID string) (string, error) {
	account, err := a.accountStore.Get(ctx, accountID)
	if err != nil {
		return "", fmt.Errorf("failed to get account: %w", err)
	}
	if account == nil {
		return "", fmt.Errorf("account not found: %s", accountID)
	}
	if account.PolicyStoreID == "" {
		return "", fmt.Errorf("account has no policy store (privileged accounts cannot have policies)")
	}
	return account.PolicyStoreID, nil
}

// CreatePolicy creates a new policy template in AVP.
// The cedarPolicy should use ?principal as the placeholder for template-linked policies.
func (a *authorizerImpl) CreatePolicy(ctx context.Context, accountID, name, description, cedarPolicy string) (*store.Policy, error) {
	if strings.TrimSpace(cedarPolicy) == "" {
		return nil, fmt.Errorf("invalid policy: cedar policy text is required")
	}

	policyStoreID, err := a.getAccountPolicyStoreID(ctx, accountID)
	if err != nil {
		return nil, err
	}

	resp, err := a.avpClient.CreatePolicyTemplate(ctx, &verifiedpermissions.CreatePolicyTemplateInput{
		PolicyStoreId: aws.String(policyStoreID),
		Statement:     aws.String(cedarPolicy),
		Description:   aws.String(encodePolicyMeta(name, description)),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create policy template: %w", err)
	}

	a.logger.Info("policy template created", "account_id", accountID, "policy_template_id", *resp.PolicyTemplateId, "name", name)

	return &store.Policy{
		AccountID:   accountID,
		PolicyID:    *resp.PolicyTemplateId,
		Name:        name,
		Description: description,
		CedarPolicy: cedarPolicy,
		CreatedAt:   resp.CreatedDate.Format(time.RFC3339),
	}, nil
}

// GetPolicy retrieves a policy template from AVP
func (a *authorizerImpl) GetPolicy(ctx context.Context, accountID, policyID string) (*store.Policy, error) {
	policyStoreID, err := a.getAccountPolicyStoreID(ctx, accountID)
	if err != nil {
		return nil, err
	}

	resp, err := a.avpClient.GetPolicyTemplate(ctx, &verifiedpermissions.GetPolicyTemplateInput{
		PolicyStoreId:    aws.String(policyStoreID),
		PolicyTemplateId: aws.String(policyID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get policy template: %w", err)
	}

	name, description := decodePolicyMeta(aws.ToString(resp.Description))

	return &store.Policy{
		AccountID:   accountID,
		PolicyID:    policyID,
		Name:        name,
		Description: description,
		CedarPolicy: aws.ToString(resp.Statement),
		CreatedAt:   resp.CreatedDate.Format(time.RFC3339),
	}, nil
}

// UpdatePolicy updates a policy template in AVP.
// AVP automatically propagates template changes to all template-linked policies.
func (a *authorizerImpl) UpdatePolicy(ctx context.Context, accountID, policyID, name, description, cedarPolicy string) (*store.Policy, error) {
	if strings.TrimSpace(cedarPolicy) == "" {
		return nil, fmt.Errorf("invalid policy: cedar policy text is required")
	}

	policyStoreID, err := a.getAccountPolicyStoreID(ctx, accountID)
	if err != nil {
		return nil, err
	}

	resp, err := a.avpClient.UpdatePolicyTemplate(ctx, &verifiedpermissions.UpdatePolicyTemplateInput{
		PolicyStoreId:    aws.String(policyStoreID),
		PolicyTemplateId: aws.String(policyID),
		Statement:        aws.String(cedarPolicy),
		Description:      aws.String(encodePolicyMeta(name, description)),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update policy template: %w", err)
	}

	a.logger.Info("policy template updated", "account_id", accountID, "policy_template_id", policyID)

	return &store.Policy{
		AccountID:   accountID,
		PolicyID:    policyID,
		Name:        name,
		Description: description,
		CedarPolicy: cedarPolicy,
		CreatedAt:   resp.CreatedDate.Format(time.RFC3339),
	}, nil
}

// DeletePolicy removes a policy template from AVP
func (a *authorizerImpl) DeletePolicy(ctx context.Context, accountID, policyID string) error {
	policyStoreID, err := a.getAccountPolicyStoreID(ctx, accountID)
	if err != nil {
		return err
	}

	// Check if policy has attachments via AVP ListPolicies
	listResp, err := a.avpClient.ListPolicies(ctx, &verifiedpermissions.ListPoliciesInput{
		PolicyStoreId: aws.String(policyStoreID),
		Filter: &avptypes.PolicyFilter{
			PolicyTemplateId: aws.String(policyID),
			PolicyType:       avptypes.PolicyTypeTemplateLinked,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to check policy attachments: %w", err)
	}
	if len(listResp.Policies) > 0 {
		return fmt.Errorf("cannot delete policy with existing attachments")
	}

	_, err = a.avpClient.DeletePolicyTemplate(ctx, &verifiedpermissions.DeletePolicyTemplateInput{
		PolicyStoreId:    aws.String(policyStoreID),
		PolicyTemplateId: aws.String(policyID),
	})
	if err != nil {
		return fmt.Errorf("failed to delete policy template: %w", err)
	}

	a.logger.Info("policy template deleted", "account_id", accountID, "policy_template_id", policyID)
	return nil
}

// ListPolicies returns all policy templates for an account from AVP
func (a *authorizerImpl) ListPolicies(ctx context.Context, accountID string) ([]*store.Policy, error) {
	policyStoreID, err := a.getAccountPolicyStoreID(ctx, accountID)
	if err != nil {
		return nil, err
	}

	resp, err := a.avpClient.ListPolicyTemplates(ctx, &verifiedpermissions.ListPolicyTemplatesInput{
		PolicyStoreId: aws.String(policyStoreID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list policy templates: %w", err)
	}

	policies := make([]*store.Policy, 0, len(resp.PolicyTemplates))
	for _, tmpl := range resp.PolicyTemplates {
		templateID := aws.ToString(tmpl.PolicyTemplateId)

		// Fetch the full template to get the statement
		detail, err := a.avpClient.GetPolicyTemplate(ctx, &verifiedpermissions.GetPolicyTemplateInput{
			PolicyStoreId:    aws.String(policyStoreID),
			PolicyTemplateId: aws.String(templateID),
		})
		if err != nil {
			a.logger.Warn("failed to get policy template detail", "error", err, "template_id", templateID)
			continue
		}

		name, description := decodePolicyMeta(aws.ToString(detail.Description))
		policies = append(policies, &store.Policy{
			AccountID:   accountID,
			PolicyID:    templateID,
			Name:        name,
			Description: description,
			CedarPolicy: aws.ToString(detail.Statement),
			CreatedAt:   detail.CreatedDate.Format(time.RFC3339),
		})
	}

	return policies, nil
}

// AttachPolicy creates a template-linked policy in AVP, binding the template
// to a concrete principal (user or group).
func (a *authorizerImpl) AttachPolicy(ctx context.Context, accountID, policyID string, targetType TargetType, targetID string) (*Attachment, error) {
	policyStoreID, err := a.getAccountPolicyStoreID(ctx, accountID)
	if err != nil {
		return nil, err
	}

	// Build principal entity for template linking
	var principalEntity *avptypes.EntityIdentifier
	switch targetType {
	case TargetTypeGroup:
		principalEntity = &avptypes.EntityIdentifier{
			EntityType: aws.String("ROSA::Group"),
			EntityId:   aws.String(targetID),
		}
	default:
		principalEntity = &avptypes.EntityIdentifier{
			EntityType: aws.String("ROSA::Principal"),
			EntityId:   aws.String(targetID),
		}
	}

	// Create template-linked policy in AVP
	avpResp, err := a.avpClient.CreatePolicy(ctx, &verifiedpermissions.CreatePolicyInput{
		PolicyStoreId: aws.String(policyStoreID),
		Definition: &avptypes.PolicyDefinitionMemberTemplateLinked{
			Value: avptypes.TemplateLinkedPolicyDefinition{
				PolicyTemplateId: aws.String(policyID),
				Principal:        principalEntity,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create template-linked policy: %w", err)
	}

	a.logger.Info("policy attached", "account_id", accountID, "policy_id", policyID, "target_type", targetType, "target_id", targetID, "avp_policy_id", *avpResp.PolicyId)

	return &Attachment{
		AttachmentID: *avpResp.PolicyId,
		PolicyID:     policyID,
		TargetType:   targetType,
		TargetID:     targetID,
		CreatedAt:    avpResp.CreatedDate.Format(time.RFC3339),
	}, nil
}

// DetachPolicy removes a policy attachment. The attachmentID is the AVP policy ID.
func (a *authorizerImpl) DetachPolicy(ctx context.Context, accountID, attachmentID string) error {
	policyStoreID, err := a.getAccountPolicyStoreID(ctx, accountID)
	if err != nil {
		return err
	}

	_, err = a.avpClient.DeletePolicy(ctx, &verifiedpermissions.DeletePolicyInput{
		PolicyStoreId: aws.String(policyStoreID),
		PolicyId:      aws.String(attachmentID),
	})
	if err != nil {
		return fmt.Errorf("failed to delete policy attachment: %w", err)
	}

	a.logger.Info("policy detached", "account_id", accountID, "avp_policy_id", attachmentID)
	return nil
}

// ListAttachments returns attachments matching the filter by querying AVP ListPolicies.
func (a *authorizerImpl) ListAttachments(ctx context.Context, accountID string, filter AttachmentFilter) ([]*Attachment, error) {
	policyStoreID, err := a.getAccountPolicyStoreID(ctx, accountID)
	if err != nil {
		return nil, err
	}

	policyFilter := &avptypes.PolicyFilter{
		PolicyType: avptypes.PolicyTypeTemplateLinked,
	}

	if filter.PolicyID != "" {
		policyFilter.PolicyTemplateId = aws.String(filter.PolicyID)
	}

	if filter.TargetType != "" && filter.TargetID != "" {
		entityType := "ROSA::Principal"
		if filter.TargetType == TargetTypeGroup {
			entityType = "ROSA::Group"
		}
		policyFilter.Principal = &avptypes.EntityReferenceMemberIdentifier{
			Value: avptypes.EntityIdentifier{
				EntityType: aws.String(entityType),
				EntityId:   aws.String(filter.TargetID),
			},
		}
	}

	listResp, err := a.avpClient.ListPolicies(ctx, &verifiedpermissions.ListPoliciesInput{
		PolicyStoreId: aws.String(policyStoreID),
		Filter:        policyFilter,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list policy attachments: %w", err)
	}

	attachments := make([]*Attachment, 0, len(listResp.Policies))
	for _, p := range listResp.Policies {
		att := &Attachment{
			AttachmentID: aws.ToString(p.PolicyId),
		}

		if p.CreatedDate != nil {
			att.CreatedAt = p.CreatedDate.Format(time.RFC3339)
		}

		// Extract template ID and principal from definition
		if tlDef, ok := p.Definition.(*avptypes.PolicyDefinitionItemMemberTemplateLinked); ok {
			att.PolicyID = aws.ToString(tlDef.Value.PolicyTemplateId)
			if tlDef.Value.Principal != nil {
				entityType := aws.ToString(tlDef.Value.Principal.EntityType)
				att.TargetID = aws.ToString(tlDef.Value.Principal.EntityId)
				if entityType == "ROSA::Group" {
					att.TargetType = TargetTypeGroup
				} else {
					att.TargetType = TargetTypeUser
				}
			}
		}

		attachments = append(attachments, att)
	}

	return attachments, nil
}

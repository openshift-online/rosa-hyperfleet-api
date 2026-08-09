package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/verifiedpermissions"
	avptypes "github.com/aws/aws-sdk-go-v2/service/verifiedpermissions/types"
	"github.com/google/uuid"
)

// mockTemplate holds a Cedar policy template in memory.
type mockTemplate struct {
	statement   string
	description string
	createdDate time.Time
}

// mockPolicy holds a resolved Cedar policy (static or template-linked) in memory.
type mockPolicy struct {
	cedarText   string                     // resolved Cedar text sent to cedar-agent
	templateID  string                     // non-empty if template-linked
	principal   *avptypes.EntityIdentifier // principal entity for template-linked policies
	createdDate time.Time
}

// MockAVPClient implements AVPClient using cedar-agent for local testing.
// It tracks policy templates and resolved policies per policy store,
// syncing resolved policies to cedar-agent before each authorization check.
type MockAVPClient struct {
	cedarAgentURL string
	httpClient    *http.Client
	logger        *slog.Logger
	mu            sync.RWMutex
	// templates tracks Cedar policy templates per policy store
	templates map[string]map[string]*mockTemplate // policyStoreID -> templateID -> template
	// policies tracks resolved Cedar policies per policy store
	policies map[string]map[string]*mockPolicy // policyStoreID -> policyID -> policy
}

// NewMockAVPClient creates a new MockAVPClient that uses cedar-agent for policy evaluation.
func NewMockAVPClient(cedarAgentURL string, logger *slog.Logger) *MockAVPClient {
	return &MockAVPClient{
		cedarAgentURL: strings.TrimSuffix(cedarAgentURL, "/"),
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		logger:        logger,
		templates:     make(map[string]map[string]*mockTemplate),
		policies:      make(map[string]map[string]*mockPolicy),
	}
}

// adaptForCedarAgent rewrites Cedar policy text for cedar-agent compatibility.
// AVP implicitly converts entity references to their IDs for string operations,
// but cedar-agent treats them as entity values. We rewrite patterns that rely
// on AVP's implicit conversion so they work with standard Cedar evaluation.
func adaptForCedarAgent(cedarText string) string {
	// "resource like" → "resource.arn like" (entity ID → attribute access)
	cedarText = strings.ReplaceAll(cedarText, "resource like ", "resource.arn like ")
	return cedarText
}

// splitCedarStatements splits multi-statement Cedar text into individual statements.
// Cedar-agent requires each policy entry to contain a single statement.
func splitCedarStatements(cedarText string) []string {
	var statements []string
	var current strings.Builder

	for line := range strings.SplitSeq(cedarText, "\n") {
		trimmed := strings.TrimSpace(line)
		if (strings.HasPrefix(trimmed, "permit") || strings.HasPrefix(trimmed, "forbid")) && current.Len() > 0 {
			if stmt := strings.TrimSpace(current.String()); stmt != "" {
				statements = append(statements, stmt)
			}
			current.Reset()
		}
		current.WriteString(line + "\n")
	}

	if stmt := strings.TrimSpace(current.String()); stmt != "" {
		statements = append(statements, stmt)
	}

	return statements
}

// syncPolicies sends the full set of resolved policies for a store to cedar-agent via PUT.
func (m *MockAVPClient) syncPolicies(ctx context.Context, policyStoreID string) error {
	m.mu.RLock()
	storePolicies := m.policies[policyStoreID]
	var policyList []map[string]string
	for id, p := range storePolicies {
		// Split multi-statement Cedar text into individual entries
		// since cedar-agent expects one statement per policy entry.
		stmts := splitCedarStatements(adaptForCedarAgent(p.cedarText))
		for i, stmt := range stmts {
			policyList = append(policyList, map[string]string{
				"id":      fmt.Sprintf("%s-%d", id, i),
				"content": stmt,
			})
		}
	}
	m.mu.RUnlock()

	if policyList == nil {
		policyList = []map[string]string{}
	}

	reqBody, err := json.Marshal(policyList)
	if err != nil {
		return fmt.Errorf("failed to marshal policies: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, m.cedarAgentURL+"/v1/policies", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create sync request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to sync policies: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sync policies failed with status %d: %s", resp.StatusCode, string(body))
	}

	m.logger.Debug("synced policies to cedar-agent", "policy_store_id", policyStoreID, "count", len(policyList))
	return nil
}

// resolvePrincipal replaces the ?principal template slot in Cedar policy text
// with a concrete principal entity. Uses "principal in" so Cedar traverses the
// entity hierarchy — group members match via ancestry, and direct matches still
// work because `A in A` is always true in Cedar.
//
// Handles both AVP template forms:
//   - "principal == ?principal" → "principal in Entity::"id""
//   - bare "?principal"        → "principal in Entity::"id""
func resolvePrincipal(cedarTemplate string, principal *avptypes.EntityIdentifier) string {
	entityType := aws.ToString(principal.EntityType)
	entityID := aws.ToString(principal.EntityId)
	principalConstraint := fmt.Sprintf(`principal in %s::"%s"`, entityType, entityID)

	if strings.Contains(cedarTemplate, "principal == ?principal") {
		return strings.ReplaceAll(cedarTemplate, "principal == ?principal", principalConstraint)
	}
	return strings.ReplaceAll(cedarTemplate, "?principal", principalConstraint)
}

// CreatePolicyStore returns a dummy policy store ID and initializes tracking.
func (m *MockAVPClient) CreatePolicyStore(ctx context.Context, params *verifiedpermissions.CreatePolicyStoreInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.CreatePolicyStoreOutput, error) {
	storeID := uuid.New().String()

	m.mu.Lock()
	m.templates[storeID] = make(map[string]*mockTemplate)
	m.policies[storeID] = make(map[string]*mockPolicy)
	m.mu.Unlock()

	m.logger.Debug("created mock policy store", "policy_store_id", storeID)

	now := time.Now()
	return &verifiedpermissions.CreatePolicyStoreOutput{
		PolicyStoreId:   aws.String(storeID),
		Arn:             aws.String(fmt.Sprintf("arn:aws:verifiedpermissions::local:policy-store/%s", storeID)),
		CreatedDate:     &now,
		LastUpdatedDate: &now,
	}, nil
}

// DeletePolicyStore removes all tracked templates and policies for the store.
func (m *MockAVPClient) DeletePolicyStore(ctx context.Context, params *verifiedpermissions.DeletePolicyStoreInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.DeletePolicyStoreOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)

	m.mu.Lock()
	delete(m.templates, storeID)
	delete(m.policies, storeID)
	m.mu.Unlock()

	return &verifiedpermissions.DeletePolicyStoreOutput{}, nil
}

// GetPolicyStore returns dummy policy store info.
func (m *MockAVPClient) GetPolicyStore(ctx context.Context, params *verifiedpermissions.GetPolicyStoreInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.GetPolicyStoreOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)
	now := time.Now()
	return &verifiedpermissions.GetPolicyStoreOutput{
		PolicyStoreId:   aws.String(storeID),
		Arn:             aws.String(fmt.Sprintf("arn:aws:verifiedpermissions::local:policy-store/%s", storeID)),
		CreatedDate:     &now,
		LastUpdatedDate: &now,
	}, nil
}

// CreatePolicyTemplate stores a policy template for later linking.
func (m *MockAVPClient) CreatePolicyTemplate(ctx context.Context, params *verifiedpermissions.CreatePolicyTemplateInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.CreatePolicyTemplateOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)
	templateID := uuid.New().String()
	now := time.Now()

	m.mu.Lock()
	if m.templates[storeID] == nil {
		m.templates[storeID] = make(map[string]*mockTemplate)
	}
	m.templates[storeID][templateID] = &mockTemplate{
		statement:   aws.ToString(params.Statement),
		description: aws.ToString(params.Description),
		createdDate: now,
	}
	m.mu.Unlock()

	m.logger.Debug("created policy template", "policy_store_id", storeID, "template_id", templateID)

	return &verifiedpermissions.CreatePolicyTemplateOutput{
		PolicyStoreId:    aws.String(storeID),
		PolicyTemplateId: aws.String(templateID),
		CreatedDate:      &now,
		LastUpdatedDate:  &now,
	}, nil
}

// GetPolicyTemplate retrieves a stored policy template.
func (m *MockAVPClient) GetPolicyTemplate(ctx context.Context, params *verifiedpermissions.GetPolicyTemplateInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.GetPolicyTemplateOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)
	templateID := aws.ToString(params.PolicyTemplateId)

	m.mu.RLock()
	tmpl, ok := m.templates[storeID][templateID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("policy template not found: %s", templateID)
	}

	now := time.Now()
	return &verifiedpermissions.GetPolicyTemplateOutput{
		PolicyStoreId:    aws.String(storeID),
		PolicyTemplateId: aws.String(templateID),
		Statement:        aws.String(tmpl.statement),
		Description:      aws.String(tmpl.description),
		CreatedDate:      &tmpl.createdDate,
		LastUpdatedDate:  &now,
	}, nil
}

// UpdatePolicyTemplate updates a template and re-resolves all linked policies.
func (m *MockAVPClient) UpdatePolicyTemplate(ctx context.Context, params *verifiedpermissions.UpdatePolicyTemplateInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.UpdatePolicyTemplateOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)
	templateID := aws.ToString(params.PolicyTemplateId)
	now := time.Now()

	m.mu.Lock()
	tmpl, ok := m.templates[storeID][templateID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("policy template not found: %s", templateID)
	}

	tmpl.statement = aws.ToString(params.Statement)
	tmpl.description = aws.ToString(params.Description)

	// Re-resolve all policies linked to this template
	for _, p := range m.policies[storeID] {
		if p.templateID == templateID {
			p.cedarText = resolvePrincipal(tmpl.statement, p.principal)
		}
	}
	m.mu.Unlock()

	if err := m.syncPolicies(ctx, storeID); err != nil {
		m.logger.Warn("failed to sync policies after template update", "error", err)
	}

	return &verifiedpermissions.UpdatePolicyTemplateOutput{
		PolicyStoreId:    aws.String(storeID),
		PolicyTemplateId: aws.String(templateID),
		CreatedDate:      &tmpl.createdDate,
		LastUpdatedDate:  &now,
	}, nil
}

// DeletePolicyTemplate removes a policy template.
func (m *MockAVPClient) DeletePolicyTemplate(ctx context.Context, params *verifiedpermissions.DeletePolicyTemplateInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.DeletePolicyTemplateOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)
	templateID := aws.ToString(params.PolicyTemplateId)

	m.mu.Lock()
	if store := m.templates[storeID]; store != nil {
		delete(store, templateID)
	}
	m.mu.Unlock()

	return &verifiedpermissions.DeletePolicyTemplateOutput{}, nil
}

// ListPolicyTemplates returns all policy templates for a store.
func (m *MockAVPClient) ListPolicyTemplates(ctx context.Context, params *verifiedpermissions.ListPolicyTemplatesInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.ListPolicyTemplatesOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)

	m.mu.RLock()
	storeTemplates := m.templates[storeID]
	items := make([]avptypes.PolicyTemplateItem, 0, len(storeTemplates))
	for id, tmpl := range storeTemplates {
		items = append(items, avptypes.PolicyTemplateItem{
			PolicyStoreId:    aws.String(storeID),
			PolicyTemplateId: aws.String(id),
			Description:      aws.String(tmpl.description),
			CreatedDate:      &tmpl.createdDate,
			LastUpdatedDate:  &tmpl.createdDate,
		})
	}
	m.mu.RUnlock()

	return &verifiedpermissions.ListPolicyTemplatesOutput{
		PolicyTemplates: items,
	}, nil
}

// CreatePolicy stores a policy (static or template-linked) and syncs to cedar-agent.
func (m *MockAVPClient) CreatePolicy(ctx context.Context, params *verifiedpermissions.CreatePolicyInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.CreatePolicyOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)
	policyID := uuid.New().String()

	var cedarText string
	var templateID string
	var principal *avptypes.EntityIdentifier
	var policyType avptypes.PolicyType

	switch def := params.Definition.(type) {
	case *avptypes.PolicyDefinitionMemberStatic:
		cedarText = aws.ToString(def.Value.Statement)
		policyType = avptypes.PolicyTypeStatic

	case *avptypes.PolicyDefinitionMemberTemplateLinked:
		templateID = aws.ToString(def.Value.PolicyTemplateId)
		policyType = avptypes.PolicyTypeTemplateLinked

		m.mu.RLock()
		tmpl, ok := m.templates[storeID][templateID]
		m.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("policy template not found: %s", templateID)
		}

		// Resolve the template with the concrete principal
		cedarText = resolvePrincipal(tmpl.statement, def.Value.Principal)
		principal = def.Value.Principal

	default:
		return nil, fmt.Errorf("unsupported policy definition type")
	}

	now := time.Now()
	m.mu.Lock()
	if m.policies[storeID] == nil {
		m.policies[storeID] = make(map[string]*mockPolicy)
	}
	m.policies[storeID][policyID] = &mockPolicy{
		cedarText:   cedarText,
		templateID:  templateID,
		principal:   principal,
		createdDate: now,
	}
	m.mu.Unlock()

	if err := m.syncPolicies(ctx, storeID); err != nil {
		m.logger.Warn("failed to sync policies after create", "error", err)
	}

	m.logger.Info("created policy", "policy_store_id", storeID, "policy_id", policyID, "type", policyType)

	return &verifiedpermissions.CreatePolicyOutput{
		PolicyStoreId:   aws.String(storeID),
		PolicyId:        aws.String(policyID),
		PolicyType:      policyType,
		CreatedDate:     &now,
		LastUpdatedDate: &now,
	}, nil
}

// DeletePolicy removes a policy and syncs remaining policies to cedar-agent.
func (m *MockAVPClient) DeletePolicy(ctx context.Context, params *verifiedpermissions.DeletePolicyInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.DeletePolicyOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)
	policyID := aws.ToString(params.PolicyId)

	m.mu.Lock()
	if store := m.policies[storeID]; store != nil {
		delete(store, policyID)
	}
	m.mu.Unlock()

	if err := m.syncPolicies(ctx, storeID); err != nil {
		m.logger.Warn("failed to sync policies after delete", "error", err)
	}

	m.logger.Debug("deleted policy", "policy_store_id", storeID, "policy_id", policyID)
	return &verifiedpermissions.DeletePolicyOutput{}, nil
}

// GetPolicy returns the stored policy.
func (m *MockAVPClient) GetPolicy(ctx context.Context, params *verifiedpermissions.GetPolicyInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.GetPolicyOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)
	policyID := aws.ToString(params.PolicyId)

	m.mu.RLock()
	p, ok := m.policies[storeID][policyID]
	m.mu.RUnlock()

	cedarText := ""
	if ok {
		cedarText = p.cedarText
	}

	now := time.Now()
	return &verifiedpermissions.GetPolicyOutput{
		PolicyStoreId: aws.String(storeID),
		PolicyId:      aws.String(policyID),
		PolicyType:    avptypes.PolicyTypeStatic,
		Definition: &avptypes.PolicyDefinitionDetailMemberStatic{
			Value: avptypes.StaticPolicyDefinitionDetail{
				Statement: aws.String(cedarText),
			},
		},
		CreatedDate:     &now,
		LastUpdatedDate: &now,
	}, nil
}

// UpdatePolicy updates a stored policy and syncs to cedar-agent.
func (m *MockAVPClient) UpdatePolicy(ctx context.Context, params *verifiedpermissions.UpdatePolicyInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.UpdatePolicyOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)
	policyID := aws.ToString(params.PolicyId)

	var cedarText string
	if staticDef, ok := params.Definition.(*avptypes.UpdatePolicyDefinitionMemberStatic); ok {
		cedarText = aws.ToString(staticDef.Value.Statement)
	} else {
		return nil, fmt.Errorf("only static policy updates are supported")
	}

	m.mu.Lock()
	if m.policies[storeID] == nil {
		m.policies[storeID] = make(map[string]*mockPolicy)
	}
	if p, ok := m.policies[storeID][policyID]; ok {
		p.cedarText = cedarText
	} else {
		m.policies[storeID][policyID] = &mockPolicy{cedarText: cedarText}
	}
	m.mu.Unlock()

	if err := m.syncPolicies(ctx, storeID); err != nil {
		m.logger.Warn("failed to sync policies after update", "error", err)
	}

	m.logger.Info("updated policy", "policy_store_id", storeID, "policy_id", policyID)

	now := time.Now()
	return &verifiedpermissions.UpdatePolicyOutput{
		PolicyStoreId:   aws.String(storeID),
		PolicyId:        aws.String(policyID),
		PolicyType:      avptypes.PolicyTypeStatic,
		CreatedDate:     &now,
		LastUpdatedDate: &now,
	}, nil
}

// ListPolicies returns policies matching optional filters (template ID, policy type, principal).
func (m *MockAVPClient) ListPolicies(ctx context.Context, params *verifiedpermissions.ListPoliciesInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.ListPoliciesOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)

	m.mu.RLock()
	storePolicies := m.policies[storeID]

	var items []avptypes.PolicyItem
	for id, p := range storePolicies {
		// Apply filter if present
		if params.Filter != nil {
			// Filter by policy template ID
			if params.Filter.PolicyTemplateId != nil {
				if p.templateID != aws.ToString(params.Filter.PolicyTemplateId) {
					continue
				}
			}

			// Filter by policy type
			if params.Filter.PolicyType != "" {
				if params.Filter.PolicyType == avptypes.PolicyTypeTemplateLinked && p.templateID == "" {
					continue
				}
				if params.Filter.PolicyType == avptypes.PolicyTypeStatic && p.templateID != "" {
					continue
				}
			}

			// Filter by principal
			if params.Filter.Principal != nil {
				if ref, ok := params.Filter.Principal.(*avptypes.EntityReferenceMemberIdentifier); ok {
					if p.principal == nil {
						continue
					}
					if aws.ToString(p.principal.EntityType) != aws.ToString(ref.Value.EntityType) ||
						aws.ToString(p.principal.EntityId) != aws.ToString(ref.Value.EntityId) {
						continue
					}
				}
			}
		}

		item := avptypes.PolicyItem{
			PolicyStoreId:   aws.String(storeID),
			PolicyId:        aws.String(id),
			CreatedDate:     &p.createdDate,
			LastUpdatedDate: &p.createdDate,
		}

		if p.templateID != "" {
			item.PolicyType = avptypes.PolicyTypeTemplateLinked
			item.Definition = &avptypes.PolicyDefinitionItemMemberTemplateLinked{
				Value: avptypes.TemplateLinkedPolicyDefinitionItem{
					PolicyTemplateId: aws.String(p.templateID),
					Principal:        p.principal,
				},
			}
			item.Principal = p.principal
		} else {
			item.PolicyType = avptypes.PolicyTypeStatic
			item.Definition = &avptypes.PolicyDefinitionItemMemberStatic{
				Value: avptypes.StaticPolicyDefinitionItem{},
			}
		}

		items = append(items, item)
	}
	m.mu.RUnlock()

	return &verifiedpermissions.ListPoliciesOutput{
		Policies: items,
	}, nil
}

// IsAuthorized syncs all policies for the store to cedar-agent, then delegates authorization.
func (m *MockAVPClient) IsAuthorized(ctx context.Context, params *verifiedpermissions.IsAuthorizedInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.IsAuthorizedOutput, error) {
	storeID := aws.ToString(params.PolicyStoreId)

	// Sync all policies for this store to cedar-agent before authorizing
	if err := m.syncPolicies(ctx, storeID); err != nil {
		m.logger.Warn("failed to sync policies before authorization", "error", err)
	}

	// Build cedar-agent request
	cedarReq := m.buildCedarAgentRequest(params)

	reqBody, err := json.Marshal(cedarReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cedar-agent request: %w", err)
	}

	m.logger.Debug("sending authorization request to cedar-agent", "request_body", string(reqBody))

	url := m.cedarAgentURL + "/v1/is_authorized"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create cedar-agent request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cedar-agent request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read cedar-agent response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cedar-agent returned status %d: %s", resp.StatusCode, string(body))
	}

	var cedarResp struct {
		Decision    string `json:"decision"`
		Diagnostics struct {
			Reason []string `json:"reason"`
			Errors []string `json:"errors"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(body, &cedarResp); err != nil {
		return nil, fmt.Errorf("failed to parse cedar-agent response: %w", err)
	}

	m.logger.Debug("received authorization response from cedar-agent",
		"decision", cedarResp.Decision,
		"reasons", cedarResp.Diagnostics.Reason,
	)

	decision := avptypes.DecisionDeny
	if strings.EqualFold(cedarResp.Decision, "allow") {
		decision = avptypes.DecisionAllow
	}

	return &verifiedpermissions.IsAuthorizedOutput{
		Decision: decision,
	}, nil
}

// PutSchema is a no-op - cedar-agent schema upload often fails due to unsupported features.
func (m *MockAVPClient) PutSchema(ctx context.Context, params *verifiedpermissions.PutSchemaInput, optFns ...func(*verifiedpermissions.Options)) (*verifiedpermissions.PutSchemaOutput, error) {
	now := time.Now()
	return &verifiedpermissions.PutSchemaOutput{
		PolicyStoreId:   params.PolicyStoreId,
		CreatedDate:     &now,
		LastUpdatedDate: &now,
	}, nil
}

// buildCedarAgentRequest converts an AVP IsAuthorizedInput to cedar-agent format.
func (m *MockAVPClient) buildCedarAgentRequest(params *verifiedpermissions.IsAuthorizedInput) map[string]any {
	req := make(map[string]any)

	var principalUID string

	// Principal: ROSA::Principal::"principal-id"
	if params.Principal != nil {
		principalType := aws.ToString(params.Principal.EntityType)
		principalID := aws.ToString(params.Principal.EntityId)
		principalUID = fmt.Sprintf("%s::\"%s\"", principalType, principalID)
		req["principal"] = principalUID
	}

	// Action: ROSA::Action::"action-name"
	if params.Action != nil {
		actionID := aws.ToString(params.Action.ActionId)
		// Strip "rosa:" prefix if present to match Cedar policy format
		actionID = strings.TrimPrefix(actionID, "rosa:")
		req["action"] = fmt.Sprintf("ROSA::Action::\"%s\"", actionID)
	}

	// Resource: ROSA::Resource::"resource-id"
	if params.Resource != nil {
		resourceType := aws.ToString(params.Resource.EntityType)
		resourceID := aws.ToString(params.Resource.EntityId)
		req["resource"] = fmt.Sprintf("%s::\"%s\"", resourceType, resourceID)
	}

	// Context
	if params.Context != nil {
		if contextMap, ok := params.Context.(*avptypes.ContextDefinitionMemberContextMap); ok {
			ctxMap := make(map[string]any)
			for key, val := range contextMap.Value {
				ctxMap[key] = convertAttributeValue(val)
			}
			req["context"] = ctxMap
		}
	}

	// Entities - build entity hierarchy for group membership and resource attributes
	var entities []map[string]any
	resourceAdded := false

	if params.Entities != nil {
		if entityList, ok := params.Entities.(*avptypes.EntitiesDefinitionMemberEntityList); ok {
			var groupUIDs []string

			for _, entity := range entityList.Value {
				entityType := aws.ToString(entity.Identifier.EntityType)
				entityID := aws.ToString(entity.Identifier.EntityId)
				uid := fmt.Sprintf("%s::\"%s\"", entityType, entityID)

				// Track group UIDs for principal parents
				if entityType == "ROSA::Group" || entityType == "Group" {
					groupUIDs = append(groupUIDs, uid)
					entities = append(entities, map[string]any{
						"uid":     uid,
						"attrs":   map[string]any{},
						"parents": []string{},
					})
				}

				// Handle resource entities with attributes (e.g., tags)
				if entity.Attributes != nil && (entityType == "ROSA::Resource" || entityType == "Resource") {
					attrs := make(map[string]any)
					for k, v := range entity.Attributes {
						attrs[k] = convertAttributeValue(v)
					}
					attrs["arn"] = entityID
					entities = append(entities, map[string]any{
						"uid":     uid,
						"attrs":   attrs,
						"parents": []string{},
					})
					resourceAdded = true
				}
			}

			// Add principal entity with group parents
			if principalUID != "" && len(groupUIDs) > 0 {
				entities = append(entities, map[string]any{
					"uid":     principalUID,
					"attrs":   map[string]any{},
					"parents": groupUIDs,
				})
			}
		}
	}

	// Add resource entity with arn attribute if not already added via entity attributes
	if params.Resource != nil && !resourceAdded {
		resourceType := aws.ToString(params.Resource.EntityType)
		resourceID := aws.ToString(params.Resource.EntityId)
		resourceUID := fmt.Sprintf("%s::\"%s\"", resourceType, resourceID)
		entities = append(entities, map[string]any{
			"uid": resourceUID,
			"attrs": map[string]any{
				"arn": resourceID,
			},
			"parents": []string{},
		})
	}

	if len(entities) > 0 {
		req["entities"] = entities
	}

	return req
}

// convertAttributeValue converts AVP AttributeValue to a Go value.
func convertAttributeValue(val avptypes.AttributeValue) any {
	switch v := val.(type) {
	case *avptypes.AttributeValueMemberString:
		return v.Value
	case *avptypes.AttributeValueMemberLong:
		return v.Value
	case *avptypes.AttributeValueMemberBoolean:
		return v.Value
	case *avptypes.AttributeValueMemberSet:
		result := make([]any, len(v.Value))
		for i, item := range v.Value {
			result[i] = convertAttributeValue(item)
		}
		return result
	case *avptypes.AttributeValueMemberRecord:
		result := make(map[string]any)
		for key, item := range v.Value {
			result[key] = convertAttributeValue(item)
		}
		return result
	default:
		return nil
	}
}

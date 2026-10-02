package authz

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/x/exp/schema/resolved"
	"github.com/cedar-policy/cedar-go/x/exp/schema/validate"
)

type evaluator func(*cedar.PolicySet, cedar.EntityMap, cedar.Request) (cedar.Decision, cedar.Diagnostic, error)

// Authorizer resolves material once per request. It never reads configuration files.
type Authorizer struct {
	resolver  PolicyResolver
	model     *resolved.Schema
	validator *validate.Validator
	evaluate  evaluator
}

// Prepared owns concrete immutable policy inputs for one identity and request.
type Prepared struct {
	identity   Identity
	principal  cedar.EntityUID
	entities   cedar.EntityMap
	policies   *cedar.PolicySet
	provenance map[cedar.PolicyID]Provenance
	validator  *validate.Validator
	evaluate   evaluator
}

func NewAuthorizer(resolver PolicyResolver) (*Authorizer, error) {
	if resolver == nil {
		return nil, fmt.Errorf("policy resolver is required")
	}
	model, v, err := loadSchema()
	if err != nil {
		return nil, newFailure(StageParsing, err)
	}
	return &Authorizer{resolver: resolver, model: model, validator: v, evaluate: nativeEvaluate}, nil
}

func nativeEvaluate(set *cedar.PolicySet, entities cedar.EntityMap, req cedar.Request) (cedar.Decision, cedar.Diagnostic, error) {
	decision, diagnostics := cedar.Authorize(set, entities, req)
	return decision, diagnostics, nil
}

func (a *Authorizer) Prepare(ctx context.Context, id Identity) (*Prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, newFailure(StageResolution, err)
	}
	caller, err := checkedIdentity(id)
	if err != nil {
		return nil, newFailure(StageResolution, err)
	}
	material, err := a.resolver.Resolve(ctx, id)
	if err != nil {
		return nil, newFailure(StageResolution, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, newFailure(StageResolution, err)
	}
	prepared := &Prepared{identity: id, principal: entityUID("Principal", id.CallerARN), entities: make(cedar.EntityMap), policies: cedar.NewPolicySet(), provenance: make(map[cedar.PolicyID]Provenance), validator: a.validator, evaluate: a.evaluate}
	for uid, action := range a.model.Actions {
		prepared.entities[uid] = action.Entity
	}
	attrs := cedar.NewRecord(cedar.RecordMap{"account": cedar.String(id.AccountID)})
	parents := []cedar.EntityUID{}
	roleAliases := make(map[string]string)
	for _, binding := range material {
		// Revalidate injected resolver material before it can become a usable set.
		if err := checkedPolicy(binding.PolicyContent, binding.PolicyID, a.validator); err != nil {
			return nil, newFailure(StageParsing, err, binding.Provenance)
		}
		target, mode, err := checkBinding(binding, id, caller)
		if err != nil {
			return nil, newFailure(StageBinding, err, binding.Provenance)
		}
		diagnosticID := cedar.PolicyID(binding.DiagnosticID)
		if _, exists := prepared.provenance[diagnosticID]; exists {
			return nil, newFailure(StageBinding, fmt.Errorf("duplicate diagnostic ID"), binding.Provenance)
		}
		bound, err := bindPolicy(binding.PolicyContent, target, mode)
		if err != nil {
			return nil, newFailure(StageBinding, err, binding.Provenance)
		}
		if err := a.validator.Policy(binding.DiagnosticID, expPolicy(bound)); err != nil {
			return nil, newFailure(StageBinding, err, binding.Provenance)
		}
		prepared.policies.Add(diagnosticID, bound)
		prepared.provenance[diagnosticID] = binding.Provenance
		if mode == roleMembership {
			if err := addRoleAlias(roleAliases, caller.roleAlias(), string(target.ID)); err != nil {
				return nil, newFailure(StageBinding, err, binding.Provenance)
			}
			prepared.entities[target] = cedar.Entity{UID: target, Attributes: attrs}
			parents = append(parents, target)
		}
	}
	prepared.entities[prepared.principal] = cedar.Entity{UID: prepared.principal, Parents: cedar.NewEntityUIDSet(parents...), Attributes: attrs}
	if err := prepared.validator.Entities(prepared.entities); err != nil {
		return nil, newFailure(StageEntityValidation, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, newFailure(StageResolution, err)
	}
	return prepared, nil
}

func checkBinding(binding ResolvedBinding, id Identity, caller principalARN) (cedar.EntityUID, bindingMode, error) {
	if !idPattern.MatchString(binding.PolicyID) || !idPattern.MatchString(binding.AttachmentID) || binding.DiagnosticID != "attachment/"+binding.AttachmentID || binding.PolicyRevision == "" || binding.AttachmentRevision == "" || binding.Caller != id || binding.OwnerAccountID != id.AccountID {
		return cedar.EntityUID{}, "", fmt.Errorf("incomplete or inconsistent binding provenance")
	}
	target, err := parsePrincipal(binding.PrincipalARN)
	if err != nil {
		return cedar.EntityUID{}, "", err
	}
	mode, err := checkedMode(target, binding.BindingMode)
	if err != nil {
		return cedar.EntityUID{}, "", err
	}
	if err := checkedScope(binding.Scope, binding.Region); err != nil {
		return cedar.EntityUID{}, "", err
	}
	if !matchesPrincipal(target, caller) || !appliesInRegion(binding.Scope, binding.Region, id.Region) {
		return cedar.EntityUID{}, "", fmt.Errorf("binding does not apply to caller account, partition, principal, or region")
	}
	kind := "Principal"
	if mode == roleMembership {
		kind = "Role"
	}
	return entityUID(kind, target.original), mode, nil
}

func (p *Prepared) Check(ctx context.Context, action Action, resource Resource) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, newFailure(StageEvaluation, err)
	}
	entities, resourceUID, err := p.resourceEntities(action, resource)
	if err != nil {
		return Decision{}, newFailure(StageEntityValidation, err)
	}
	req := cedar.Request{Principal: p.principal, Action: entityUID("Action", string(action)), Resource: resourceUID, Context: cedar.NewRecord(cedar.RecordMap{
		"region": cedar.String(p.identity.Region), "accountId": cedar.String(p.identity.AccountID), "principalArn": cedar.String(p.identity.CallerARN),
	})}
	if err := p.validator.Entities(entities); err != nil {
		return Decision{}, newFailure(StageEntityValidation, err)
	}
	if err := p.validator.Request(req); err != nil {
		return Decision{}, newFailure(StageEntityValidation, err)
	}
	decision, diagnostics, err := p.evaluate(p.policies, entities, req)
	if err != nil {
		return Decision{}, newFailure(StageEvaluation, err)
	}
	// Native Cedar may allow despite a failing forbid. Errors always take precedence.
	if len(diagnostics.Errors) > 0 {
		failure := newFailure(StageEvaluation, errors.New("cedar evaluation diagnostics"))
		failure.Diagnostics = append([]cedar.DiagnosticError(nil), diagnostics.Errors...)
		ids := make([]cedar.PolicyID, 0, len(diagnostics.Errors))
		for _, diagnostic := range diagnostics.Errors {
			ids = append(ids, diagnostic.PolicyID)
		}
		var mappingErr error
		failure.Provenance, mappingErr = p.mapProvenance(ids)
		failure.Err = errors.Join(failure.Err, mappingErr)
		return Decision{}, failure
	}
	if err := ctx.Err(); err != nil {
		return Decision{}, newFailure(StageEvaluation, err)
	}
	ids := make([]cedar.PolicyID, 0, len(diagnostics.Reasons))
	for _, reason := range diagnostics.Reasons {
		ids = append(ids, reason.PolicyID)
	}
	provenance, err := p.mapProvenance(ids)
	if err != nil {
		return Decision{}, newFailure(StageEvaluation, err)
	}
	return Decision{Allowed: decision == cedar.Allow, Provenance: provenance}, nil
}

func (p *Prepared) mapProvenance(ids []cedar.PolicyID) ([]Provenance, error) {
	slices.Sort(ids)
	ids = slices.Compact(ids)
	provenance := make([]Provenance, 0, len(ids))
	var mappingErr error
	for _, id := range ids {
		binding, exists := p.provenance[id]
		if !exists {
			mappingErr = errors.Join(mappingErr, fmt.Errorf("unknown diagnostic policy ID %q", id))
			continue
		}
		provenance = append(provenance, binding)
	}
	return provenance, mappingErr
}

func (p *Prepared) resourceEntities(action Action, resource Resource) (cedar.EntityMap, cedar.EntityUID, error) {
	if resource.AccountID != p.identity.AccountID || resource.Region != p.identity.Region {
		return nil, cedar.EntityUID{}, fmt.Errorf("resource ownership does not match identity account and region")
	}
	if owner, exists := resource.Labels["hyperfleet.io/account-id"]; exists && owner != resource.AccountID {
		return nil, cedar.EntityUID{}, fmt.Errorf("resource account label contradicts ownership")
	}
	entities := maps.Clone(p.entities)
	attrs := cedar.NewRecord(cedar.RecordMap{"account": cedar.String(resource.AccountID), "region": cedar.String(resource.Region)})
	collection := entityUID("Collection", resource.AccountID+"/"+resource.Region+"/clusters")
	entities[collection] = cedar.Entity{UID: collection, Attributes: attrs}
	switch resource.Kind {
	case Collection:
		if resource.ID != "" || len(resource.Labels) != 0 || action != ListClusters {
			return nil, cedar.EntityUID{}, fmt.Errorf("collection requires ListClusters, no ID, and no labels")
		}
		return entities, collection, nil
	case Cluster:
		if !idPattern.MatchString(resource.ID) || action != DescribeCluster {
			return nil, cedar.EntityUID{}, fmt.Errorf("cluster requires a stable ID and DescribeCluster")
		}
		cluster := entityUID("Cluster", resource.AccountID+"/"+resource.Region+"/"+resource.ID)
		tags := make(cedar.RecordMap, len(resource.Labels))
		for key, value := range resource.Labels {
			tags[cedar.String(key)] = cedar.String(value)
		}
		entities[cluster] = cedar.Entity{UID: cluster, Attributes: attrs, Parents: cedar.NewEntityUIDSet(collection), Tags: cedar.NewRecord(tags)}
		return entities, cluster, nil
	default:
		return nil, cedar.EntityUID{}, fmt.Errorf("unsupported resource kind %q", resource.Kind)
	}
}

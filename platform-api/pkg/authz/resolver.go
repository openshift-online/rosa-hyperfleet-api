package authz

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type configBundle struct {
	FormatVersion      int                `yaml:"formatVersion"`
	RegisteredAccounts []string           `yaml:"registeredAccounts"`
	Policies           []policyRecord     `yaml:"policies"`
	Attachments        []attachmentRecord `yaml:"attachments"`
}

type policyRecord struct {
	ID             string `yaml:"id"`
	OwnerAccountID string `yaml:"ownerAccountID"`
	Content        string `yaml:"content"`
}

type attachmentRecord struct {
	ID           string      `yaml:"id"`
	PolicyID     string      `yaml:"policyID"`
	PrincipalARN string      `yaml:"principalARN"`
	BindingMode  bindingMode `yaml:"bindingMode"`
	Scope        string      `yaml:"scope"`
	Region       string      `yaml:"region"`
}

type configuredBinding struct {
	material  ResolvedBinding
	principal principalARN
}

// ConfigResolver owns an immutable startup snapshot. Resolve never rereads its file.
type ConfigResolver struct {
	accounts map[string]struct{}
	bindings []configuredBinding
}

func LoadConfig(path string) (*ConfigResolver, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, newFailure(StageResolution, err)
	}
	bundle, err := decodeBundle(content)
	if err != nil {
		return nil, newFailure(StageParsing, err)
	}
	_, v, err := loadSchema()
	if err != nil {
		return nil, newFailure(StageParsing, err)
	}
	revision := fmt.Sprintf("%x", sha256.Sum256(content))
	resolver := &ConfigResolver{accounts: make(map[string]struct{})}
	for _, account := range bundle.RegisteredAccounts {
		if !accountPattern.MatchString(account) {
			return nil, newFailure(StageParsing, fmt.Errorf("invalid registered account %q", account))
		}
		if _, exists := resolver.accounts[account]; exists {
			return nil, newFailure(StageParsing, fmt.Errorf("duplicate registered account %q", account))
		}
		resolver.accounts[account] = struct{}{}
	}
	policies := make(map[string]policyRecord, len(bundle.Policies))
	for _, policy := range bundle.Policies {
		provenance := Provenance{PolicyID: policy.ID, PolicyRevision: revision}
		if !idPattern.MatchString(policy.ID) || !accountPattern.MatchString(policy.OwnerAccountID) {
			return nil, newFailure(StageParsing, fmt.Errorf("invalid policy ID or owner"), provenance)
		}
		if _, exists := policies[policy.ID]; exists {
			return nil, newFailure(StageParsing, fmt.Errorf("duplicate policy ID %q", policy.ID), provenance)
		}
		if err := checkedPolicy(policy.Content, policy.ID, v); err != nil {
			return nil, newFailure(StageParsing, err, provenance)
		}
		policies[policy.ID] = policy
	}
	ids := make(map[string]struct{}, len(bundle.Attachments))
	roleAliases := make(map[string]string)
	for _, attachment := range bundle.Attachments {
		provenance := Provenance{DiagnosticID: "attachment/" + attachment.ID, PolicyID: attachment.PolicyID, PolicyRevision: revision, AttachmentID: attachment.ID, AttachmentRevision: revision, PrincipalARN: attachment.PrincipalARN, Scope: attachment.Scope, Region: attachment.Region}
		if !idPattern.MatchString(attachment.ID) {
			return nil, newFailure(StageBinding, fmt.Errorf("invalid attachment ID"), provenance)
		}
		if _, exists := ids[attachment.ID]; exists {
			return nil, newFailure(StageBinding, fmt.Errorf("duplicate attachment ID"), provenance)
		}
		ids[attachment.ID] = struct{}{}
		policy, exists := policies[attachment.PolicyID]
		if !exists {
			return nil, newFailure(StageBinding, fmt.Errorf("dangling policy reference"), provenance)
		}
		principal, err := parsePrincipal(attachment.PrincipalARN)
		if err != nil {
			return nil, newFailure(StageBinding, err, provenance)
		}
		if principal.account != policy.OwnerAccountID {
			return nil, newFailure(StageBinding, fmt.Errorf("cross-account attachment"), provenance)
		}
		mode, err := checkedMode(principal, string(attachment.BindingMode))
		if err != nil {
			return nil, newFailure(StageBinding, err, provenance)
		}
		if err := checkedScope(attachment.Scope, attachment.Region); err != nil {
			return nil, newFailure(StageBinding, err, provenance)
		}
		if principal.kind == "role" {
			if err := addRoleAlias(roleAliases, principal.roleAlias(), principal.original); err != nil {
				return nil, newFailure(StageBinding, err, provenance)
			}
		}
		// Validate all bindings, not only those active in the local region.
		target := entityUID("Principal", principal.original)
		if mode == roleMembership {
			target = entityUID("Role", principal.original)
		}
		bound, err := bindPolicy(policy.Content, target, mode)
		if err != nil {
			return nil, newFailure(StageBinding, err, provenance)
		}
		if err := v.Policy(provenance.DiagnosticID, expPolicy(bound)); err != nil {
			return nil, newFailure(StageBinding, err, provenance)
		}
		resolver.bindings = append(resolver.bindings, configuredBinding{material: ResolvedBinding{Provenance: provenance, OwnerAccountID: policy.OwnerAccountID, PolicyContent: policy.Content, BindingMode: string(mode)}, principal: principal})
	}
	return resolver, nil
}

func (r *ConfigResolver) Resolve(ctx context.Context, id Identity) ([]ResolvedBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, newFailure(StageResolution, err)
	}
	caller, err := checkedIdentity(id)
	if err != nil {
		return nil, newFailure(StageResolution, err)
	}
	bindings := make([]ResolvedBinding, 0)
	for _, binding := range r.bindings {
		if !matchesPrincipal(binding.principal, caller) || !appliesInRegion(binding.material.Scope, binding.material.Region, id.Region) {
			continue
		}
		material := binding.material
		material.Caller = id
		bindings = append(bindings, material)
	}
	return bindings, nil
}

func (r *ConfigResolver) IsAccountRegistered(ctx context.Context, account string) bool {
	if ctx.Err() != nil {
		return false
	}
	_, exists := r.accounts[account]
	return exists
}

func decodeBundle(content []byte) (configBundle, error) {
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&root); err != nil {
		return configBundle{}, err
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return configBundle{}, fmt.Errorf("expected exactly one YAML document")
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return configBundle{}, fmt.Errorf("bundle must be an object")
	}
	if err := checkYAML(root.Content[0], "", 0); err != nil {
		return configBundle{}, err
	}
	required := map[string]bool{"formatVersion": false, "registeredAccounts": false, "policies": false, "attachments": false}
	for i := 0; i < len(root.Content[0].Content); i += 2 {
		required[root.Content[0].Content[i].Value] = true
	}
	for key, present := range required {
		if !present {
			return configBundle{}, fmt.Errorf("missing bundle field %q", key)
		}
	}
	// KnownFields also checks nested records. The node pass forbids YAML coercions and aliases.
	decoder = yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	var bundle configBundle
	if err := decoder.Decode(&bundle); err != nil {
		return configBundle{}, err
	}
	if bundle.FormatVersion != 1 {
		return configBundle{}, fmt.Errorf("unsupported bundle format version %d", bundle.FormatVersion)
	}
	return bundle, nil
}

// checkYAML rejects YAML shortcuts and coercions to keep authorization config explicit.
// It also limits nesting and requires top-level collections to be lists.
func checkYAML(node *yaml.Node, path string, depth int) error {
	if depth > 16 || node.Kind == yaml.AliasNode || node.Anchor != "" {
		return fmt.Errorf("YAML aliases, anchors, or excessive nesting at %s", path)
	}
	switch node.Kind {
	case yaml.MappingNode:
		if node.Tag != "!!map" {
			return fmt.Errorf("unsupported YAML mapping tag at %s", path)
		}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "<<" || key.Anchor != "" {
				return fmt.Errorf("invalid YAML key at %s", path)
			}
			childPath := key.Value
			if path != "" {
				childPath = path + "." + key.Value
			}
			if err := checkYAML(node.Content[i+1], childPath, depth+1); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		if node.Tag != "!!seq" {
			return fmt.Errorf("unsupported YAML sequence tag at %s", path)
		}
		for _, child := range node.Content {
			if err := checkYAML(child, path+"[]", depth+1); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		if path == "formatVersion" && node.Tag == "!!int" && node.Value == "1" {
			return nil
		}
		if node.Tag != "!!str" || path == "formatVersion" {
			return fmt.Errorf("expected string scalar at %s", path)
		}
	default:
		return fmt.Errorf("unsupported YAML node at %s", path)
	}
	// Null lists and scalar substitutes for collections are not valid bundle shapes.
	if !strings.Contains(path, ".") && !strings.Contains(path, "[]") && (path == "registeredAccounts" || path == "policies" || path == "attachments") && node.Kind != yaml.SequenceNode {
		return fmt.Errorf("expected sequence at %s", path)
	}
	return nil
}

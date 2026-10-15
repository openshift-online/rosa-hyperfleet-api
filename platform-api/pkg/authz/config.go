package authz

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"

	cedar "github.com/cedar-policy/cedar-go"
	"gopkg.in/yaml.v3"
)

type configBundle struct {
	FormatVersion              int                `yaml:"formatVersion"`
	RegisteredAccounts         []string           `yaml:"registeredAccounts"`
	Policies                   []policyRecord     `yaml:"policies"`
	Attachments                []attachmentRecord `yaml:"attachments"`
	ServiceOperatorPolicies    []policyRecord     `yaml:"serviceOperatorPolicies"`
	ServiceOperatorAttachments []attachmentRecord `yaml:"serviceOperatorAttachments"`
}

type policyRecord struct {
	ID             string `yaml:"id"`
	OwnerAccountID string `yaml:"ownerAccountID"`
	Content        string `yaml:"content"`
}

type attachmentRecord struct {
	ID           string `yaml:"id"`
	PolicyID     string `yaml:"policyID"`
	PrincipalARN string `yaml:"principalARN"`
	Scope        string `yaml:"scope"`
	Region       string `yaml:"region"`
}

type policyAttachment struct {
	Provenance
	policy    *cedar.Policy
	principal principalARN
	scope     string
	region    string
}

// LoadConfig validates a startup snapshot for a fixed service region.
// The file hash identifies its revision. Requests never reread the file.
func LoadConfig(path, serviceRegion string) (*Authorizer, error) {
	if !regionPattern.MatchString(serviceRegion) {
		return nil, newFailure(StageParsing, fmt.Errorf("invalid service region"))
	}
	model, v, err := loadSchema()
	if err != nil {
		return nil, newFailure(StageParsing, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, newFailure(StageResolution, err)
	}
	revision := fmt.Sprintf("%x", sha256.Sum256(content))
	bundle, err := decodeBundle(content)
	if err != nil {
		return nil, newFailure(StageParsing, err)
	}
	authorizer := &Authorizer{region: serviceRegion, accounts: make(map[string]struct{}), model: model, validator: v, evaluate: nativeEvaluate}
	if err := authorizer.addBundle(bundle, revision); err != nil {
		return nil, err
	}
	return authorizer, nil
}

// addBundle validates all records, including off-region and unattached material.
// Customer and service-operator policies and attachments stay separate.
func (a *Authorizer) addBundle(bundle configBundle, revision string) error {
	for _, account := range bundle.RegisteredAccounts {
		if !accountPattern.MatchString(account) {
			return newFailure(StageParsing, fmt.Errorf("invalid registered account %q", account))
		}
		if _, exists := a.accounts[account]; exists {
			return newFailure(StageParsing, fmt.Errorf("duplicate registered account %q", account))
		}
		a.accounts[account] = struct{}{}
	}
	roleAliases := make(map[string]string)
	for _, domain := range []struct {
		policies    []policyRecord
		attachments []attachmentRecord
		prefix      string
		bindings    *[]policyAttachment
	}{
		{bundle.Policies, bundle.Attachments, "attachment/", &a.customer},
		{bundle.ServiceOperatorPolicies, bundle.ServiceOperatorAttachments, "service-operator/attachment/", &a.service},
	} {
		type compiledPolicy struct {
			owner  string
			policy *cedar.Policy
		}
		policies := make(map[string]compiledPolicy, len(domain.policies))
		for _, policy := range domain.policies {
			provenance := Provenance{PolicyID: policy.ID, PolicyRevision: revision}
			if !idPattern.MatchString(policy.ID) || !accountPattern.MatchString(policy.OwnerAccountID) {
				return newFailure(StageParsing, fmt.Errorf("invalid policy ID or owner"), provenance)
			}
			if _, exists := policies[policy.ID]; exists {
				return newFailure(StageParsing, fmt.Errorf("duplicate policy ID %q", policy.ID), provenance)
			}
			compiled, err := checkedPolicy(policy.Content, policy.ID, a.validator)
			if err != nil {
				return newFailure(StageParsing, err, provenance)
			}
			policies[policy.ID] = compiledPolicy{owner: policy.OwnerAccountID, policy: compiled}
		}
		ids := make(map[string]struct{}, len(domain.attachments))
		for _, attachment := range domain.attachments {
			provenance := Provenance{DiagnosticID: domain.prefix + attachment.ID, PolicyID: attachment.PolicyID, PolicyRevision: revision, AttachmentID: attachment.ID}
			if !idPattern.MatchString(attachment.ID) {
				return newFailure(StageBinding, fmt.Errorf("invalid attachment ID"), provenance)
			}
			if _, exists := ids[attachment.ID]; exists {
				return newFailure(StageBinding, fmt.Errorf("duplicate attachment ID"), provenance)
			}
			ids[attachment.ID] = struct{}{}
			policy, exists := policies[attachment.PolicyID]
			if !exists {
				return newFailure(StageBinding, fmt.Errorf("dangling policy reference"), provenance)
			}
			principal, err := parsePrincipal(attachment.PrincipalARN)
			if err != nil {
				return newFailure(StageBinding, err, provenance)
			}
			if principal.account != policy.owner {
				return newFailure(StageBinding, fmt.Errorf("cross-account attachment"), provenance)
			}
			if err := checkedScope(attachment.Scope, attachment.Region); err != nil {
				return newFailure(StageBinding, err, provenance)
			}
			if principal.kind == "role" {
				if err := addRoleAlias(roleAliases, principal.roleAlias(), principal.original); err != nil {
					return newFailure(StageBinding, err, provenance)
				}
			}
			*domain.bindings = append(*domain.bindings, policyAttachment{Provenance: provenance, policy: policy.policy, principal: principal, scope: attachment.Scope, region: attachment.Region})
		}
	}
	return nil
}

// readBundleNode requires one YAML document whose root is an object.
// It checks the raw YAML structure before decoding fields into Go structs.
func readBundleNode(content []byte) (*yaml.Node, error) {
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one YAML document")
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("bundle must be an object")
	}
	if err := checkYAML(root.Content[0], "", 0); err != nil {
		return nil, err
	}
	return root.Content[0], nil
}

// decodeBundle decodes a version-1 bundle, requiring enrollment, policies, and attachments.
// It rejects unknown fields and requires the optional service-operator lists together.
func decodeBundle(content []byte) (configBundle, error) {
	root, err := readBundleNode(content)
	if err != nil {
		return configBundle{}, err
	}
	required := map[string]bool{"formatVersion": false, "registeredAccounts": false, "policies": false, "attachments": false}
	for i := 0; i < len(root.Content); i += 2 {
		required[root.Content[i].Value] = true
	}
	for key, present := range required {
		if !present {
			return configBundle{}, fmt.Errorf("missing bundle field %q", key)
		}
	}
	if required["serviceOperatorPolicies"] != required["serviceOperatorAttachments"] {
		return configBundle{}, fmt.Errorf("service operator policies and attachments must be supplied together")
	}
	// KnownFields also checks nested records. The node pass forbids YAML coercions and aliases.
	decoder := yaml.NewDecoder(bytes.NewReader(content))
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
	switch path {
	case "registeredAccounts", "policies", "attachments", "serviceOperatorPolicies", "serviceOperatorAttachments":
		if node.Kind != yaml.SequenceNode {
			return fmt.Errorf("expected sequence at %s", path)
		}
	}
	return nil
}

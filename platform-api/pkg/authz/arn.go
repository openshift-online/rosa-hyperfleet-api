package authz

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
)

var (
	accountPattern = regexp.MustCompile(`^[0-9]{12}$`)
	regionPattern  = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)+-[0-9]+$`)
	namePattern    = regexp.MustCompile(`^[A-Za-z0-9+=,.@_-]{1,64}$`)
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

type principalARN struct {
	partition string
	account   string
	kind      string
	roleName  string
	original  string
}

// parsePrincipal validates a commercial AWS IAM user, IAM role, or STS assumed-role ARN.
// It preserves the full ARN and extracts the fields needed for principal matching.
func parsePrincipal(value string) (principalARN, error) {
	parsed, err := arn.Parse(value)
	if err != nil {
		return principalARN{}, err
	}
	switch parsed.Partition {
	// TODO: Should we support partitions beyond commercial AWS?
	// case "aws", "aws-cn", "aws-us-gov", "aws-iso", "aws-iso-b", "aws-iso-e", "aws-iso-f", "aws-eusc":
	case "aws":
	default:
		return principalARN{}, fmt.Errorf("unsupported ARN partition %q", parsed.Partition)
	}
	if parsed.Region != "" || !accountPattern.MatchString(parsed.AccountID) {
		return principalARN{}, fmt.Errorf("principal ARN requires an account and no region")
	}
	parts := strings.Split(parsed.Resource, "/")
	if len(parts) < 2 {
		return principalARN{}, fmt.Errorf("unsupported principal ARN resource")
	}
	principal := principalARN{partition: parsed.Partition, account: parsed.AccountID, kind: parts[0], original: value}
	switch {
	case parsed.Service == "iam" && (principal.kind == "user" || principal.kind == "role"):
		if len(parsed.Resource) > 512 {
			return principalARN{}, fmt.Errorf("principal ARN path too long")
		}
		for _, part := range parts[1:] {
			if !namePattern.MatchString(part) || part == "." || part == ".." {
				return principalARN{}, fmt.Errorf("invalid principal ARN path")
			}
		}
		if principal.kind == "role" {
			principal.roleName = parts[len(parts)-1]
		}
	case parsed.Service == "sts" && principal.kind == "assumed-role":
		if len(parts) != 3 || !namePattern.MatchString(parts[1]) || !namePattern.MatchString(parts[2]) {
			return principalARN{}, fmt.Errorf("invalid assumed-role ARN")
		}
		principal.roleName = parts[1]
	default:
		return principalARN{}, fmt.Errorf("unsupported principal ARN service or kind")
	}
	return principal, nil
}

// checkedIdentity validates that the caller is an IAM user or assumed-role session in the claimed account.
func checkedIdentity(id Identity) (principalARN, error) {
	principal, err := parsePrincipal(id.CallerARN)
	if err != nil {
		return principalARN{}, err
	}
	if principal.account != id.AccountID || principal.kind == "role" {
		return principalARN{}, fmt.Errorf("caller must be a user or session in the identity account")
	}
	return principal, nil
}

// roleAlias identifies a role by partition, account, and final role name.
// It omits the IAM role path because STS assumed-role ARNs do not include it.
func (p principalARN) roleAlias() string { return p.partition + "/" + p.account + "/" + p.roleName }

// addRoleAlias records the full role ARN for an alias and rejects conflicting role paths.
// This prevents ambiguous session-to-role matching when STS omits the path.
func addRoleAlias(aliases map[string]string, alias, role string) error {
	if previous, exists := aliases[alias]; exists && previous != role {
		return fmt.Errorf("ambiguous role alias %q", alias)
	}
	aliases[alias] = role
	return nil
}

// matchesPrincipal accepts an exact ARN match or an assumed-role session matching a role attachment.
// Role matching requires the same partition, account, and final role name.
func matchesPrincipal(target, caller principalARN) bool {
	if target.original == caller.original {
		return true
	}
	return target.kind == "role" && caller.kind == "assumed-role" && target.roleAlias() == caller.roleAlias()
}

// checkedScope requires global attachments to omit a region and regional attachments to specify a valid one.
func checkedScope(scope, region string) error {
	if scope == "global" && region == "" {
		return nil
	}
	if scope == "regional" && regionPattern.MatchString(region) {
		return nil
	}
	return fmt.Errorf("invalid attachment scope or region")
}

// appliesInRegion accepts global attachments everywhere and regional attachments only in their configured region.
// The scope and region must already have passed checkedScope.
func appliesInRegion(scope, attachmentRegion, requestRegion string) bool {
	return scope == "global" || (scope == "regional" && attachmentRegion == requestRegion)
}

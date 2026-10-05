// Package iamauth holds the rules for logging in to hosted clusters with AWS IAM
// (STS outbound identity federation tokens). The Platform API uses them to
// validate cluster create requests and the hyperfleet-operator uses them to
// render the HostedCluster authentication configuration, so both sides agree.
package iamauth

import (
	"fmt"
	"regexp"
	"strings"
)

// IssuerURLPattern matches AWS STS outbound identity federation issuers. Each
// hosted kube-apiserver fetches signing keys from this URL from inside the
// management cluster network, so no other host may ever be accepted.
const IssuerURLPattern = `^https://[a-z0-9-]+\.tokens\.sts\.global\.api\.aws$`

var (
	issuerURLRE = regexp.MustCompile(IssuerURLPattern)
	accountIDRE = regexp.MustCompile(`^\d{12}$`)
	partitionRE = regexp.MustCompile(`^aws(-[a-z]+)*$`)
	// IAM role names: alphanumerics and +=,.@_- up to 64 characters.
	roleNameRE = regexp.MustCompile(`^[\w+=,.@-]{1,64}$`)
	// IAM user ARN resources: user/[path/]name with the same character set.
	userResourceRE = regexp.MustCompile(`^user/(?:[\w+=,.@-]+/)*[\w+=,.@-]{1,64}$`)
)

// ValidIssuerURL reports whether u is an AWS STS outbound identity federation issuer.
func ValidIssuerURL(u string) bool {
	return issuerURLRE.MatchString(u)
}

// ValidAccountID reports whether id is a 12-digit AWS account ID.
func ValidAccountID(id string) bool {
	return accountIDRE.MatchString(id)
}

// ClusterAudience is the token audience a hosted cluster accepts. Binding tokens
// to one cluster stops them from being replayed against other clusters in the
// same AWS account.
func ClusterAudience(clusterID string) string {
	return "rosa:cluster:" + clusterID
}

// CreatorSubjectCondition returns a CEL condition that is true when an STS
// token's "sub" claim is the principal that made a request as callerARN.
//
// API Gateway reports role sessions as arn:aws:sts::<acct>:assumed-role/<name>/<session>,
// while tokens carry sub = arn:aws:iam::<acct>:role/[<path>/]<name>. Role names are
// unique within an account regardless of path, so the condition matches the
// account and name with any path. IAM users are matched exactly. Other principals
// (root, federated users) are rejected. Every interpolated value is restricted to
// characters that need no escaping in a CEL string.
func CreatorSubjectCondition(callerARN string) (string, error) {
	parts := strings.SplitN(callerARN, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" {
		return "", fmt.Errorf("%q is not an ARN", callerARN)
	}
	partition, service, account, resource := parts[1], parts[2], parts[4], parts[5]
	if !partitionRE.MatchString(partition) || !ValidAccountID(account) {
		return "", fmt.Errorf("%q has an invalid partition or account", callerARN)
	}

	switch {
	case service == "sts" && strings.HasPrefix(resource, "assumed-role/"):
		role := strings.Split(resource, "/")
		if len(role) != 3 || !roleNameRE.MatchString(role[1]) {
			return "", fmt.Errorf("%q is not a valid assumed-role ARN", callerARN)
		}
		return fmt.Sprintf("claims.sub.startsWith('arn:%s:iam::%s:role/') && claims.sub.endsWith('/%s')", partition, account, role[1]), nil
	case service == "iam" && userResourceRE.MatchString(resource):
		return fmt.Sprintf("claims.sub == '%s'", callerARN), nil
	default:
		return "", fmt.Errorf("%q cannot be granted cluster-admin: only IAM roles and IAM users are supported", callerARN)
	}
}

package render

import (
	"fmt"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/api/iamauth"
	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
)

const (
	// stsClaims is the namespace AWS STS nests its own claims under.
	stsClaims = "claims['https://sts.amazonaws.com/']"
	// clusterAdminsGroup is bound to cluster-admin by OpenShift's cluster-admins ClusterRoleBinding.
	clusterAdminsGroup = "system:cluster-admins"
	// maxTokenLifetimeSeconds rejects tokens minted with a longer DurationSeconds.
	maxTokenLifetimeSeconds = 900
)

// awsIAMAuthentication renders the HostedCluster authentication configuration
// that lets principals of the cluster's AWS account log in to the kube-apiserver
// with STS outbound identity federation tokens. The cluster creator is
// cluster-admin; everyone else needs in-cluster RBAC on the "aws:<ARN>" username.
// It returns nil when AWS IAM login is not enabled for the cluster.
func awsIAMAuthentication(cluster *hyperfleetv1alpha1.Cluster, clusterID string) (*configv1.AuthenticationSpec, error) {
	spec := cluster.Spec
	if spec.AWSIAMLoginIssuerURL == "" {
		return nil, nil
	}

	// Every value below is interpolated into CEL or fetched by the kube-apiserver
	// from the management cluster network, so validate again here even though the
	// Platform API already did.
	if !iamauth.ValidIssuerURL(spec.AWSIAMLoginIssuerURL) {
		return nil, fmt.Errorf("awsIAMLoginIssuerURL %q is not an AWS STS outbound identity federation issuer", spec.AWSIAMLoginIssuerURL)
	}
	if !iamauth.ValidAccountID(spec.AccountID) {
		return nil, fmt.Errorf("accountId %q is not a 12-digit AWS account ID", spec.AccountID)
	}
	creatorPattern, err := iamauth.CreatorSubjectPattern(spec.CreatorARN)
	if err != nil {
		return nil, fmt.Errorf("mapping cluster creator: %w", err)
	}
	// The pattern is embedded in a single-quoted raw CEL string.
	if strings.ContainsAny(creatorPattern, "'\n") {
		return nil, fmt.Errorf("creator pattern %q cannot be embedded in CEL", creatorPattern)
	}

	provider := configv1.OIDCProvider{
		Name: "aws-iam",
		Issuer: configv1.TokenIssuer{
			URL:       spec.AWSIAMLoginIssuerURL,
			Audiences: []configv1.TokenAudience{configv1.TokenAudience(iamauth.ClusterAudience(clusterID))},
		},
		ClaimMappings: configv1.TokenClaimMappings{
			// The "aws:" prefix keeps IAM identities out of the system: namespace.
			Username: configv1.UsernameClaimMapping{Expression: "'aws:' + claims.sub"},
			// Groups only ever come from the service-controlled creator match,
			// never from request or session tags the caller could set.
			Groups: configv1.PrefixedClaimMapping{TokenClaimMapping: configv1.TokenClaimMapping{
				Expression: fmt.Sprintf("claims.sub.matches(r'%s') ? ['%s'] : []", creatorPattern, clusterAdminsGroup),
			}},
			UID: &configv1.TokenClaimOrExpressionMapping{Claim: "sub"},
			// Everyone assuming a role shares one username; record the human for audit.
			Extra: []configv1.ExtraMapping{{
				Key:             "rosa.openshift.io/source-identity",
				ValueExpression: fmt.Sprintf("has(%[1]s.source_identity) ? %[1]s.source_identity : ''", stsClaims),
			}},
		},
		ClaimValidationRules: []configv1.TokenClaimValidationRule{
			// Binds the cluster to its AWS account. This is the control that keeps
			// other accounts out, so it must never be removed.
			celClaimRule(fmt.Sprintf("%s.aws_account == '%s'", stsClaims, spec.AccountID), "token is from a different AWS account"),
			celClaimRule(fmt.Sprintf("claims.exp - claims.iat <= %d", maxTokenLifetimeSeconds), "token lifetime exceeds 15 minutes"),
		},
		UserValidationRules: []configv1.TokenUserValidationRule{{
			Expression: "!user.username.startsWith('system:')",
			Message:    "reserved username",
		}},
	}

	return &configv1.AuthenticationSpec{
		Type:          configv1.AuthenticationTypeOIDC,
		OIDCProviders: []configv1.OIDCProvider{provider},
	}, nil
}

func celClaimRule(expression, message string) configv1.TokenClaimValidationRule {
	return configv1.TokenClaimValidationRule{
		Type: configv1.TokenValidationRuleTypeCEL,
		CEL:  configv1.TokenClaimValidationCELRule{Expression: expression, Message: message},
	}
}

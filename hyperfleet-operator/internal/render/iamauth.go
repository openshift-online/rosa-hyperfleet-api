package render

import (
	"fmt"

	"github.com/openshift-online/rosa-hyperfleet-api/api/iamauth"
	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
)

const (
	// stsClaims is the namespace AWS STS nests its own claims under.
	stsClaims = "claims['https://sts.amazonaws.com/']"
	// clusterAdminsGroup is bound to cluster-admin by OpenShift's cluster-admins ClusterRoleBinding.
	clusterAdminsGroup = "system:cluster-admins"
	// usernamePrefix is prepended to the token's sub (the IAM principal ARN).
	usernamePrefix = "aws:"
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

	// The issuer is fetched by the kube-apiserver from the management cluster
	// network and the other values are interpolated into CEL, so validate them
	// again here even though the Platform API already did.
	if !iamauth.ValidIssuerURL(spec.AWSIAMLoginIssuerURL) {
		return nil, fmt.Errorf("awsIAMLoginIssuerURL %q is not an AWS STS outbound identity federation issuer", spec.AWSIAMLoginIssuerURL)
	}
	if !iamauth.ValidAccountID(spec.AccountID) {
		return nil, fmt.Errorf("accountId %q is not a 12-digit AWS account ID", spec.AccountID)
	}
	isCreator, err := iamauth.CreatorSubjectCondition(spec.CreatorARN)
	if err != nil {
		return nil, fmt.Errorf("mapping cluster creator: %w", err)
	}

	return &configv1.AuthenticationSpec{
		Type: configv1.AuthenticationTypeOIDC,
		OIDCProviders: []configv1.OIDCProvider{{
			Name: "aws-iam",
			Issuer: configv1.TokenIssuer{
				URL:       spec.AWSIAMLoginIssuerURL,
				Audiences: []configv1.TokenAudience{configv1.TokenAudience(iamauth.ClusterAudience(clusterID))},
			},
			// oidcClients and username.prefix lack omitempty; unset they serialize
			// as null, which the HostedCluster CRD rejects.
			OIDCClients: []configv1.OIDCClientConfig{},
			ClaimMappings: configv1.TokenClaimMappings{
				Username: configv1.UsernameClaimMapping{
					Claim:        "sub",
					PrefixPolicy: configv1.Prefix,
					Prefix:       &configv1.UsernamePrefix{PrefixString: usernamePrefix},
				},
				Groups: configv1.PrefixedClaimMapping{TokenClaimMapping: configv1.TokenClaimMapping{
					Expression: fmt.Sprintf("%s ? ['%s'] : []", isCreator, clusterAdminsGroup),
				}},
			},
			// The issuer URL is not proven to belong to the cluster's account, so
			// this rule is what keeps other AWS accounts out.
			ClaimValidationRules: []configv1.TokenClaimValidationRule{{
				Type: configv1.TokenValidationRuleTypeCEL,
				CEL: configv1.TokenClaimValidationCELRule{
					Expression: fmt.Sprintf("%s.aws_account == '%s'", stsClaims, spec.AccountID),
					Message:    "token is from a different AWS account",
				},
			}},
		}},
	}, nil
}

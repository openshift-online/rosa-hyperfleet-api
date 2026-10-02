package render

import (
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

const testIAMLoginIssuer = "https://a1b2c3d4-e5f6.tokens.sts.global.api.aws"

func testClusterWithIAMLogin() *hyperfleetv1alpha1.Cluster {
	c := testCluster()
	c.Spec.AccountID = "123456789012"
	c.Spec.CreatorARN = "arn:aws:sts::123456789012:assumed-role/PlatformAdmins/alice"
	c.Spec.AWSIAMLoginIssuerURL = testIAMLoginIssuer
	return c
}

func renderedHostedCluster(t *testing.T, c *hyperfleetv1alpha1.Cluster) *hypershiftv1beta1.HostedCluster {
	t.Helper()
	resources, err := ClusterResources(c, false, "f7a3.0.example.com")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	for _, m := range resources {
		if m.Resource == "hostedclusters" {
			return m.Object.(*hypershiftv1beta1.HostedCluster)
		}
	}
	t.Fatal("no hostedcluster resource found")
	return nil
}

func TestHostedClusterAWSIAMLogin(t *testing.T) {
	t.Run("When AWS IAM login is not enabled it should not configure authentication", func(t *testing.T) {
		hc := renderedHostedCluster(t, testCluster())
		if hc.Spec.Configuration != nil && hc.Spec.Configuration.Authentication != nil {
			t.Errorf("expected no authentication config, got %+v", hc.Spec.Configuration.Authentication)
		}
	})

	t.Run("When AWS IAM login is enabled it should trust only the account issuer for this cluster", func(t *testing.T) {
		authn := renderedHostedCluster(t, testClusterWithIAMLogin()).Spec.Configuration.Authentication
		if authn == nil || authn.Type != configv1.AuthenticationTypeOIDC || len(authn.OIDCProviders) != 1 {
			t.Fatalf("expected one OIDC provider, got %+v", authn)
		}
		issuer := authn.OIDCProviders[0].Issuer
		if issuer.URL != testIAMLoginIssuer {
			t.Errorf("issuerURL = %q, want %q", issuer.URL, testIAMLoginIssuer)
		}
		if len(issuer.Audiences) != 1 || issuer.Audiences[0] != "rosa:cluster:abc12345" {
			t.Errorf("audiences = %v, want [rosa:cluster:abc12345]", issuer.Audiences)
		}
	})

	t.Run("When AWS IAM login is enabled it should reject tokens from other accounts", func(t *testing.T) {
		provider := renderedHostedCluster(t, testClusterWithIAMLogin()).Spec.Configuration.Authentication.OIDCProviders[0]
		want := "claims['https://sts.amazonaws.com/'].aws_account == '123456789012'"
		for _, rule := range provider.ClaimValidationRules {
			if rule.Type == configv1.TokenValidationRuleTypeCEL && rule.CEL.Expression == want {
				return
			}
		}
		t.Errorf("missing account validation rule %q in %+v", want, provider.ClaimValidationRules)
	})

	t.Run("When AWS IAM login is enabled it should make only the creator cluster-admin", func(t *testing.T) {
		mappings := renderedHostedCluster(t, testClusterWithIAMLogin()).Spec.Configuration.Authentication.OIDCProviders[0].ClaimMappings
		want := `claims.sub.matches(r'^arn:aws:iam::123456789012:role/(?:[^:]*/)?PlatformAdmins$') ? ['system:cluster-admins'] : []`
		if mappings.Groups.Expression != want {
			t.Errorf("groups expression = %q, want %q", mappings.Groups.Expression, want)
		}
		if mappings.Username.Expression != "'aws:' + claims.sub" {
			t.Errorf("username expression = %q", mappings.Username.Expression)
		}
	})

	t.Run("When AWS IAM login is enabled it should keep the API server configuration", func(t *testing.T) {
		hc := renderedHostedCluster(t, testClusterWithIAMLogin())
		if hc.Spec.Configuration.APIServer == nil {
			t.Error("expected apiServer configuration to be kept")
		}
	})

	failures := []struct {
		name    string
		mutate  func(c *hyperfleetv1alpha1.Cluster)
		wantErr string
	}{
		{
			name:    "When the issuer is not an AWS STS issuer it should fail",
			mutate:  func(c *hyperfleetv1alpha1.Cluster) { c.Spec.AWSIAMLoginIssuerURL = "https://169.254.169.254" },
			wantErr: "not an AWS STS",
		},
		{
			name:    "When the account ID is invalid it should fail",
			mutate:  func(c *hyperfleetv1alpha1.Cluster) { c.Spec.AccountID = "1234' || true" },
			wantErr: "12-digit",
		},
		{
			name:    "When the creator is the account root it should fail",
			mutate:  func(c *hyperfleetv1alpha1.Cluster) { c.Spec.CreatorARN = "arn:aws:iam::123456789012:root" },
			wantErr: "creator",
		},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			c := testClusterWithIAMLogin()
			tt.mutate(c)
			_, err := ClusterResources(c, false, "f7a3.0.example.com")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

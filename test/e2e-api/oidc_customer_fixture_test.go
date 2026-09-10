package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// oidcSigningKeySecretPath mirrors hyperfleet-operator/internal/oidc.SecretName, which the e2e-api
// test module can't import directly (different module, internal package).
func oidcSigningKeySecretPath(accountID, configID string) string {
	return fmt.Sprintf("/hyperfleet/oidc/%s/%s/signing-key", accountID, configID)
}

// customerOidcFixture is a self-provisioned stand-in for a customer's unmanaged OIDC infrastructure:
// a real RSA signing key in Secrets Manager, plus an IAM role the operator assumes to read it.
type customerOidcFixture struct {
	sm      *secretsmanager.Client
	iamCli  *iam.Client
	KeyPEM  []byte
	Secret  string // ARN
	RoleArn string
}

// isPermissionDenied reports whether err is an AWS permission-denied error (vs. some other setup failure).
func isPermissionDenied(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "AccessDenied", "AccessDeniedException", "UnauthorizedException", "NotAuthorizedException":
		return true
	default:
		return false
	}
}

// provisionCustomerOidcFixture provisions a Secrets Manager secret + IAM role standing in for a customer account.
// Returns (nil, skipReason) only on a permission error; other failures fail the spec directly, not as a skip.
func provisionCustomerOidcFixture(ctx context.Context, name string) (*customerOidcFixture, string) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		// Missing AWS config/credentials is a prerequisite issue, still skip-worthy.
		return nil, fmt.Sprintf("loading AWS config: %v", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred(), "generating RSA key for customer OIDC fixture")
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	f := &customerOidcFixture{
		sm:     secretsmanager.NewFromConfig(cfg),
		iamCli: iam.NewFromConfig(cfg),
		KeyPEM: keyPEM,
	}

	secretOut, err := f.sm.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String("e2e-" + name),
		SecretString: aws.String(string(keyPEM)),
		Description:  aws.String("e2e-api OIDC lifecycle test fixture - safe to delete"),
	})
	if err != nil {
		if isPermissionDenied(err) {
			return nil, fmt.Sprintf("creating fixture secret (need secretsmanager:CreateSecret): %v", err)
		}
		Expect(err).NotTo(HaveOccurred(), "creating customer OIDC fixture secret")
	}
	f.Secret = aws.ToString(secretOut.ARN)

	trustedPrincipal := os.Getenv("E2E_OIDC_TRUSTED_PRINCIPAL_ARN")
	if trustedPrincipal == "" {
		if secretAccount, ok := accountIDFromARN(f.Secret); ok {
			trustedPrincipal = "arn:aws:iam::" + secretAccount + ":root"
		}
	}
	trustPolicy, _ := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":    "Allow",
				"Principal": map[string]any{"AWS": trustedPrincipal},
				"Action":    []string{"sts:AssumeRole", "sts:TagSession"},
			},
		},
	})

	roleTagKey := os.Getenv("E2E_OIDC_ROLE_TAG_KEY")
	if roleTagKey == "" {
		roleTagKey = "hyperfleet-access"
	}
	roleTagValue := os.Getenv("E2E_OIDC_ROLE_TAG_VALUE")
	if roleTagValue == "" {
		roleTagValue = "true"
	}
	roleOut, err := f.iamCli.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("e2e-" + name),
		AssumeRolePolicyDocument: aws.String(string(trustPolicy)),
		Description:              aws.String("e2e-api OIDC lifecycle test fixture - safe to delete"),
		Tags: []iamtypes.Tag{
			{Key: aws.String(roleTagKey), Value: aws.String(roleTagValue)},
		},
	})
	if err != nil {
		if _, delErr := f.sm.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{SecretId: aws.String(f.Secret), ForceDeleteWithoutRecovery: aws.Bool(true)}); delErr != nil {
			GinkgoWriter.Printf("customerOidcFixture: rollback DeleteSecret(%s) after CreateRole failure failed: %v\n", f.Secret, delErr)
		}
		if isPermissionDenied(err) {
			return nil, fmt.Sprintf("creating fixture role (need iam:CreateRole): %v", err)
		}
		Expect(err).NotTo(HaveOccurred(), "creating customer OIDC fixture role")
	}
	f.RoleArn = aws.ToString(roleOut.Role.Arn)

	readPolicy, _ := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{"Effect": "Allow", "Action": "secretsmanager:GetSecretValue", "Resource": f.Secret},
		},
	})
	if _, err := f.iamCli.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       roleOut.Role.RoleName,
		PolicyName:     aws.String("read-signing-key"),
		PolicyDocument: aws.String(string(readPolicy)),
	}); err != nil {
		f.cleanup(ctx)
		if isPermissionDenied(err) {
			return nil, fmt.Sprintf("attaching fixture role policy: %v", err)
		}
		Expect(err).NotTo(HaveOccurred(), "attaching customer OIDC fixture role policy")
	}

	// IAM role/policy propagation is eventually consistent; give it a head start before the
	// operator's first cross-account read attempt so we don't burn its 30s Pending retry on it.
	time.Sleep(10 * time.Second)

	return f, ""
}

// cleanup tears down the fixture's IAM role/policy and secret, logging (not discarding) any failures.
func (f *customerOidcFixture) cleanup(ctx context.Context) {
	if f == nil {
		return
	}
	if f.iamCli != nil && f.RoleArn != "" {
		if name, ok := roleNameFromARN(f.RoleArn); ok {
			if _, err := f.iamCli.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{RoleName: aws.String(name), PolicyName: aws.String("read-signing-key")}); err != nil {
				GinkgoWriter.Printf("customerOidcFixture cleanup: DeleteRolePolicy(role=%s) failed: %v\n", name, err)
			}
			if _, err := f.iamCli.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(name)}); err != nil {
				GinkgoWriter.Printf("customerOidcFixture cleanup: DeleteRole(%s) failed: %v\n", name, err)
			}
		}
	}
	if f.sm != nil && f.Secret != "" {
		if _, err := f.sm.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{SecretId: aws.String(f.Secret), ForceDeleteWithoutRecovery: aws.Bool(true)}); err != nil {
			GinkgoWriter.Printf("customerOidcFixture cleanup: DeleteSecret(%s) failed: %v\n", f.Secret, err)
		}
	}
}

// accountIDFromARN extracts the account ID segment from an ARN (arn:aws:service:region:account:resource).
func accountIDFromARN(arn string) (string, bool) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 5 || parts[4] == "" {
		return "", false
	}
	return parts[4], true
}

// roleNameFromARN extracts the role name from an IAM role ARN (arn:aws:iam::account:role/name).
func roleNameFromARN(arn string) (string, bool) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return "", false
	}
	name, ok := strings.CutPrefix(parts[5], "role/")
	return name, ok
}

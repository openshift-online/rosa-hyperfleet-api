/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package oidc

import (
	"context"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	minKeyBits = 2048
)

// SecretName returns the AWS Secrets Manager path for accountID's configID OIDC signing key
func SecretName(accountID, configID string) string {
	return "/hyperfleet/oidc/" + accountID + "/" + configID + "/signing-key"
}

// InfraClient abstracts the OIDC infrastructure operations needed by the
// OidcConfig controller.
type InfraClient interface {
	StorePrivateKey(ctx context.Context, accountID, configID string, privateKeyPEM []byte) error
	PrivateKeyExists(ctx context.Context, accountID, configID string) (bool, error)
	ReadCrossAccountSecret(ctx context.Context, secretARN, roleARN string) ([]byte, error)
	DeletePrivateKey(ctx context.Context, accountID, configID string) error
	ComputeThumbprint(ctx context.Context, issuerURL string) (string, error)
}

// ValidateRSAPrivateKey checks that pemData is a valid PEM-encoded RSA private key with a modulus of at least minKeyBits
func ValidateRSAPrivateKey(pemData []byte) error {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return fmt.Errorf("no PEM block found")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return checkRSAKeySize(key)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("not a valid RSA private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("key is not RSA")
	}
	return checkRSAKeySize(rsaKey)
}

func checkRSAKeySize(key *rsa.PrivateKey) error {
	if bits := key.N.BitLen(); bits < minKeyBits {
		return fmt.Errorf("RSA key size %d bits is below the minimum of %d bits", bits, minKeyBits)
	}
	return nil
}

// AWSClient implements InfraClient using AWS Secrets Manager and STS.
type AWSClient struct {
	sm     *secretsmanager.Client
	sts    *sts.Client
	awsCfg aws.Config

	mu              sync.Mutex
	assumeRoleCache map[string]*aws.CredentialsCache
}

// NewAWSClient creates a new AWSClient.
func NewAWSClient(awsCfg aws.Config) *AWSClient {
	return &AWSClient{
		sm:              secretsmanager.NewFromConfig(awsCfg),
		sts:             sts.NewFromConfig(awsCfg),
		awsCfg:          awsCfg,
		assumeRoleCache: make(map[string]*aws.CredentialsCache),
	}
}

// assumeRoleCredentials returns a cached credentials provider for roleARN, creating and caching one on first use.
func (c *AWSClient) assumeRoleCredentials(roleARN string) *aws.CredentialsCache {
	c.mu.Lock()
	defer c.mu.Unlock()

	if creds, ok := c.assumeRoleCache[roleARN]; ok {
		return creds
	}
	creds := aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(c.sts, roleARN))
	c.assumeRoleCache[roleARN] = creds
	return creds
}

// StorePrivateKey creates the Secrets Manager secret holding the OIDC
// signing key, or overwrites it if one already exists.
func (c *AWSClient) StorePrivateKey(ctx context.Context, accountID, configID string, privateKeyPEM []byte) error {
	secretName := SecretName(accountID, configID)
	_, err := c.sm.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(secretName),
		SecretString: aws.String(string(privateKeyPEM)),
		Description:  aws.String("OIDC signing key for config " + configID),
	})
	if err != nil {
		var existsErr *smtypes.ResourceExistsException
		if !errors.As(err, &existsErr) {
			return fmt.Errorf("create secret: %w", err)
		}
		if _, err := c.sm.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
			SecretId:     aws.String(secretName),
			SecretString: aws.String(string(privateKeyPEM)),
		}); err != nil {
			return fmt.Errorf("update secret: %w", err)
		}
	}
	return nil
}

func (c *AWSClient) PrivateKeyExists(ctx context.Context, accountID, configID string) (bool, error) {
	secretName := SecretName(accountID, configID)
	_, err := c.sm.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(secretName),
	})
	if err != nil {
		var notFoundErr *smtypes.ResourceNotFoundException
		if errors.As(err, &notFoundErr) {
			return false, nil
		}
		return false, fmt.Errorf("describe secret: %w", err)
	}
	return true, nil
}

func (c *AWSClient) ReadCrossAccountSecret(ctx context.Context, secretARN, roleARN string) ([]byte, error) {
	crossSM := secretsmanager.NewFromConfig(c.awsCfg, func(o *secretsmanager.Options) {
		o.Credentials = c.assumeRoleCredentials(roleARN)
	})

	result, err := crossSM.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretARN),
	})
	if err != nil {
		return nil, fmt.Errorf("read cross-account secret: %w", err)
	}
	if result.SecretBinary != nil {
		return result.SecretBinary, nil
	}
	return []byte(aws.ToString(result.SecretString)), nil
}

func (c *AWSClient) DeletePrivateKey(ctx context.Context, accountID, configID string) error {
	secretName := SecretName(accountID, configID)
	_, err := c.sm.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
		SecretId:                   aws.String(secretName),
		ForceDeleteWithoutRecovery: aws.Bool(true),
	})
	if err != nil {
		var notFoundErr *smtypes.ResourceNotFoundException
		if errors.As(err, &notFoundErr) {
			return nil
		}
		return fmt.Errorf("delete secret: %w", err)
	}
	return nil
}

// ComputeThumbprint confirms issuerURL is TLS-reachable and returns its root CA certificate's SHA-1 thumbprint.
func (c *AWSClient) ComputeThumbprint(ctx context.Context, issuerURL string) (string, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	hostname, dialAddr, err := resolveIssuerHost(dialCtx, issuerURL)
	if err != nil {
		return "", err
	}

	return dialThumbprint(dialCtx, hostname, dialAddr)
}

// dialThumbprint TLS-dials dialAddr and returns the peer's root CA certificate SHA-1 thumbprint.
func dialThumbprint(ctx context.Context, hostname, dialAddr string) (string, error) {
	tlsDialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: hostname,
		},
	}
	conn, err := tlsDialer.DialContext(ctx, "tcp", dialAddr)
	if err != nil {
		return "", fmt.Errorf("TLS dial %s: %w", hostname, err)
	}
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return "", fmt.Errorf("unexpected connection type for %s", hostname)
	}

	return thumbprintFromChain(tlsConn.ConnectionState().PeerCertificates)
}

// thumbprintFromChain returns the SHA-1 fingerprint of certs' root CA certificate.
func thumbprintFromChain(certs []*x509.Certificate) (string, error) {
	if len(certs) == 0 {
		return "", fmt.Errorf("no TLS certificates presented")
	}
	root := certs[len(certs)-1]
	fingerprint := sha1.Sum(root.Raw) //nolint:gosec // SHA-1 required by AWS IAM OIDC provider API contract
	return fmt.Sprintf("%x", fingerprint[:]), nil
}

// resolveIssuerHost validates issuerURL against SSRF and resolves it to a concrete dial address
func resolveIssuerHost(ctx context.Context, issuerURL string) (hostname, dialAddr string, err error) {
	u, err := url.Parse(issuerURL)
	if err != nil {
		return "", "", fmt.Errorf("parse issuer URL: %w", err)
	}
	if u.Scheme != "https" {
		return "", "", fmt.Errorf("issuer URL must use https scheme, got %q", u.Scheme)
	}

	hostname = u.Hostname()
	if hostname == "" {
		return "", "", fmt.Errorf("issuer URL has no host")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return "", "", fmt.Errorf("resolve issuer host %s: %w", hostname, err)
	}
	if len(addrs) == 0 {
		return "", "", fmt.Errorf("issuer host %s did not resolve to any address", hostname)
	}
	for _, addr := range addrs {
		if isDisallowedIssuerIP(addr.IP) {
			return "", "", fmt.Errorf("issuer host %s resolves to disallowed address %s", hostname, addr.IP)
		}
	}

	return hostname, net.JoinHostPort(addrs[0].IP.String(), port), nil
}

func isDisallowedIssuerIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified()
}

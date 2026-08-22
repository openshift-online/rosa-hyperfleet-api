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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	keyBits      = 4096
	minKeyBits   = 2048
	secretPrefix = "hyperfleet/oidc/"

	discoveryPath = ".well-known/openid-configuration"
	jwksPath      = "keys.json"
)

// InfraClient abstracts the OIDC infrastructure operations needed by the
// OidcConfig controller.
type InfraClient interface {
	GenerateKeyPair() (privateKeyPEM []byte, jwksDoc []byte, err error)
	UploadOIDCDocuments(ctx context.Context, configID string, jwksDoc []byte) error
	DeleteOIDCDocuments(ctx context.Context, configID string) error
	StorePrivateKey(ctx context.Context, configID string, privateKeyPEM []byte) error
	PrivateKeyExists(ctx context.Context, configID string) (bool, error)
	ReadCrossAccountSecret(ctx context.Context, secretARN, roleARN string) ([]byte, error)
	DeletePrivateKey(ctx context.Context, configID string) error
	IssuerURL(configID string) string
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

// Config holds configuration for the OIDC infrastructure client.
type Config struct {
	S3Bucket      string
	IssuerBaseURL string
}

// AWSClient implements InfraClient using AWS S3, Secrets Manager, and STS.
type AWSClient struct {
	s3     *s3.Client
	sm     *secretsmanager.Client
	sts    *sts.Client
	awsCfg aws.Config
	config Config

	mu              sync.Mutex
	assumeRoleCache map[string]*aws.CredentialsCache
}

// NewAWSClient creates a new AWSClient.
func NewAWSClient(awsCfg aws.Config, config Config) *AWSClient {
	return &AWSClient{
		s3:              s3.NewFromConfig(awsCfg),
		sm:              secretsmanager.NewFromConfig(awsCfg),
		sts:             sts.NewFromConfig(awsCfg),
		awsCfg:          awsCfg,
		config:          config,
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

func (c *AWSClient) GenerateKeyPair() ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return nil, nil, fmt.Errorf("generate RSA key: %w", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	jwksDoc, err := buildJWKS(&key.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("build JWKS: %w", err)
	}

	return privPEM, jwksDoc, nil
}

func (c *AWSClient) UploadOIDCDocuments(ctx context.Context, configID string, jwksDoc []byte) error {
	issuer := c.IssuerURL(configID)
	discovery, err := buildDiscoveryDocument(issuer)
	if err != nil {
		return fmt.Errorf("build discovery document: %w", err)
	}

	discoKey := configID + "/" + discoveryPath
	if _, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.config.S3Bucket),
		Key:         aws.String(discoKey),
		Body:        bytes.NewReader(discovery),
		ContentType: aws.String("application/json"),
	}); err != nil {
		return fmt.Errorf("upload discovery document: %w", err)
	}

	jwksKey := configID + "/" + jwksPath
	if _, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.config.S3Bucket),
		Key:         aws.String(jwksKey),
		Body:        bytes.NewReader(jwksDoc),
		ContentType: aws.String("application/json"),
	}); err != nil {
		return fmt.Errorf("upload JWKS: %w", err)
	}

	return nil
}

func (c *AWSClient) DeleteOIDCDocuments(ctx context.Context, configID string) error {
	keys := []string{
		configID + "/" + discoveryPath,
		configID + "/" + jwksPath,
	}
	var errs []error
	for _, key := range keys {
		if _, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(c.config.S3Bucket),
			Key:    aws.String(key),
		}); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", key, err))
		}
	}
	return errors.Join(errs...)
}

// StorePrivateKey creates the Secrets Manager secret holding the OIDC
// signing key, or overwrites it if one already exists.
func (c *AWSClient) StorePrivateKey(ctx context.Context, configID string, privateKeyPEM []byte) error {
	secretName := secretPrefix + configID
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

func (c *AWSClient) PrivateKeyExists(ctx context.Context, configID string) (bool, error) {
	secretName := secretPrefix + configID
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

func (c *AWSClient) DeletePrivateKey(ctx context.Context, configID string) error {
	secretName := secretPrefix + configID
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

func (c *AWSClient) IssuerURL(configID string) string {
	return strings.TrimRight(c.config.IssuerBaseURL, "/") + "/" + configID
}

func (c *AWSClient) ComputeThumbprint(ctx context.Context, issuerURL string) (string, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	hostname, dialAddr, err := resolveIssuerHost(dialCtx, issuerURL)
	if err != nil {
		return "", err
	}

	tlsDialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: hostname,
		},
	}
	conn, err := tlsDialer.DialContext(dialCtx, "tcp", dialAddr)
	if err != nil {
		return "", fmt.Errorf("TLS dial %s: %w", hostname, err)
	}
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return "", fmt.Errorf("unexpected connection type for %s", hostname)
	}

	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("no TLS certificates from %s", hostname)
	}

	// Use the root CA certificate (last in chain) per AWS IAM OIDC provider convention.
	root := certs[len(certs)-1]
	// AWS IAM OIDC providers require the thumbprint to be a SHA-1 fingerprint of
	// the root CA certificate; this is an API contract, not a security choice.
	fingerprint := sha1.Sum(root.Raw) //nolint:gosec // SHA-1 required by AWS IAM OIDC provider API contract
	return fmt.Sprintf("%x", fingerprint[:]), nil
}

// resolveIssuerHost validates issuerURL against SSRF and resolves it to a
// concrete dial address. issuerURL is customer-controlled for unmanaged
// OidcConfigs, so it must not be allowed to reach loopback, link-local
// (including the 169.254.169.254 cloud metadata endpoint), private, or
// other non-public addresses.
//
// The hostname is resolved here and the resulting IP is used directly for
// the TLS dial in ComputeThumbprint, rather than letting the dialer
// re-resolve the hostname. This closes the DNS-rebinding TOCTOU window
// where a second lookup could return a different, unvalidated address.
// The original hostname is still returned for TLS SNI and certificate
// hostname verification.
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

// isDisallowedIssuerIP rejects loopback, link-local, private, unspecified,
// and multicast addresses to prevent the operator from being used as an
// SSRF proxy against internal infrastructure or the cloud metadata service.
func isDisallowedIssuerIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified()
}

// --- JWKS / discovery document helpers ---

type jwkKey struct {
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

func buildJWKS(pub *rsa.PublicKey) ([]byte, error) {
	derPub, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	kidHash := sha256.Sum256(derPub)
	kid := fmt.Sprintf("%x", kidHash[:])

	doc := jwksDocument{
		Keys: []jwkKey{{
			Kty: "RSA",
			Alg: "RS256",
			Use: "sig",
			Kid: kid,
			N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	}
	return json.Marshal(doc)
}

type oidcDiscoveryDocument struct {
	Issuer                           string   `json:"issuer"`
	JWKSURI                          string   `json:"jwks_uri"`
	AuthorizationEndpoint            string   `json:"authorization_endpoint"`
	ResponseTypesSupported           []string `json:"response_types_supported"`
	SubjectTypesSupported            []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
	ClaimsSupported                  []string `json:"claims_supported"`
}

func buildDiscoveryDocument(issuerURL string) ([]byte, error) {
	doc := oidcDiscoveryDocument{
		Issuer:                           issuerURL,
		JWKSURI:                          issuerURL + "/" + jwksPath,
		AuthorizationEndpoint:            "urn:kubernetes:programmatic_authorization",
		ResponseTypesSupported:           []string{"id_token"},
		SubjectTypesSupported:            []string{"public"},
		IDTokenSigningAlgValuesSupported: []string{"RS256"},
		ClaimsSupported:                  []string{"sub", "iss"},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal discovery document: %w", err)
	}
	return data, nil
}
